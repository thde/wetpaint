package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/thde/wetpaint/server/internal/protocol"
)

// Valkey is the Store shared by a fleet of instances.
//
// Keys, all under "wetpaint:":
//
//	page:<n>              JSON PageInfo, expires after PageTTL without a heartbeat
//	lb:<n>:<board>        sorted set, member → score, one member per life
//	lb:<n>:<board>:at     sorted set, the same members → unix milliseconds
//	lb:global:<board>     sorted set, one member per name+colour, best score
//	snap:<n>              paint grid snapshot, expires after SnapshotTTL
type Valkey struct {
	c valkey.Client
}

var errBadMember = errors.New("store: bad leaderboard member")

const prefix = "wetpaint:"

// ValkeyOptions says how to reach a Valkey server.
type ValkeyOptions struct {
	Addr     string // host:port
	Username string // empty: the default user
	Password string
	TLS      *tls.Config // nil: plain TCP
}

// NewValkey connects to the Valkey server described by opts and checks that
// it answers.
func NewValkey(ctx context.Context, opts ValkeyOptions) (*Valkey, error) {
	addr := opts.Addr
	c, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr},
		Username:    opts.Username,
		Password:    opts.Password,
		TLSConfig:   opts.TLS,
		// One standalone server. Without this the client probes for a
		// cluster and, if it finds one, refuses the cross-slot MGET and
		// transactions below.
		ForceSingleClient: true,
		// Nothing is read through the client-side cache, so don't ask the
		// server to track keys for it.
		DisableCache: true,
	})
	if err != nil {
		return nil, fmt.Errorf("store: connect to valkey at %s: %w", addr, err)
	}
	if err := c.Do(ctx, c.B().Ping().Build()).Error(); err != nil {
		c.Close()
		return nil, fmt.Errorf("store: ping valkey at %s: %w", addr, err)
	}
	return &Valkey{c: c}, nil
}

func pageKey(page uint32) string { return prefix + "page:" + strconv.FormatUint(uint64(page), 10) }
func snapKey(page uint32) string { return prefix + "snap:" + strconv.FormatUint(uint64(page), 10) }
func hourKey(page uint32, b Board) string {
	return fmt.Sprintf("%slb:%d:%s", prefix, page, b)
}
func globalKey(b Board) string { return prefix + "lb:global:" + b.String() }

// tx runs cmds atomically in MULTI/EXEC and returns the first error, whether
// the transaction as a whole failed or one of its commands did.
func (v *Valkey) tx(ctx context.Context, cmds ...valkey.Completed) error {
	all := make(valkey.Commands, 0, len(cmds)+2)
	all = append(all, v.c.B().Multi().Build())
	all = append(all, cmds...)
	all = append(all, v.c.B().Exec().Build())
	resps := v.c.DoMulti(ctx, all...)
	for _, r := range resps {
		if err := r.Error(); err != nil {
			return fmt.Errorf("transaction: %w", err)
		}
	}
	results, err := resps[len(resps)-1].ToArray()
	if err != nil {
		return fmt.Errorf("transaction result: %w", err)
	}
	for _, r := range results {
		if err := r.Error(); err != nil {
			return fmt.Errorf("transaction command: %w", err)
		}
	}
	return nil
}

// ClaimPage implements Store.
func (v *Valkey) ClaimPage(ctx context.Context, addr string) (uint32, error) {
	for page := uint32(1); ; page++ {
		data, err := json.Marshal(PageInfo{Page: page, Addr: addr})
		if err != nil {
			return 0, fmt.Errorf("store: encode page info: %w", err)
		}
		err = v.c.Do(ctx, v.c.B().Set().Key(pageKey(page)).Value(valkey.BinaryString(data)).Nx().Px(PageTTL).Build()).Error()
		if err == nil {
			return page, nil
		}
		if !valkey.IsValkeyNil(err) { // nil reply: the key exists, try the next page
			return 0, fmt.Errorf("store: claim page %d: %w", page, err)
		}
	}
}

// Heartbeat implements Store.
func (v *Valkey) Heartbeat(ctx context.Context, info PageInfo) error {
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("store: encode page info: %w", err)
	}
	if err := v.c.Do(ctx, v.c.B().Set().Key(pageKey(info.Page)).Value(valkey.BinaryString(data)).Px(PageTTL).Build()).Error(); err != nil {
		return fmt.Errorf("store: heartbeat page %d: %w", info.Page, err)
	}
	return nil
}

// RemovePage implements Store.
func (v *Valkey) RemovePage(ctx context.Context, page uint32) error {
	if err := v.c.Do(ctx, v.c.B().Del().Key(pageKey(page)).Build()).Error(); err != nil {
		return fmt.Errorf("store: remove page %d: %w", page, err)
	}
	return nil
}

// Pages implements Store.
func (v *Valkey) Pages(ctx context.Context) ([]PageInfo, error) {
	var keys []string
	for cursor := uint64(0); ; {
		e, err := v.c.Do(ctx, v.c.B().Scan().Cursor(cursor).Match(prefix+"page:*").Count(100).Build()).AsScanEntry()
		if err != nil {
			return nil, fmt.Errorf("store: scan pages: %w", err)
		}
		keys = append(keys, e.Elements...)
		if cursor = e.Cursor; cursor == 0 {
			break
		}
	}
	if len(keys) == 0 {
		return nil, nil
	}
	values, err := v.c.Do(ctx, v.c.B().Mget().Key(keys...).Build()).ToArray()
	if err != nil {
		return nil, fmt.Errorf("store: read pages: %w", err)
	}
	var out []PageInfo
	for _, val := range values {
		s, err := val.ToString()
		if valkey.IsValkeyNil(err) { // the key expired between SCAN and MGET
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("store: read pages: %w", err)
		}
		var info PageInfo
		if err := json.Unmarshal([]byte(s), &info); err != nil {
			return nil, fmt.Errorf("store: decode page info: %w", err)
		}
		out = append(out, info)
	}
	return out, nil
}

// Members encode the colour and name. Hourly members also carry the time of
// death so that every life is its own member.
func globalMember(e protocol.Entry) string {
	return fmt.Sprintf("%02x%02x%02x|%s", e.Color.R, e.Color.G, e.Color.B, e.Name)
}

func hourMember(e protocol.Entry, at time.Time) string {
	return strconv.FormatInt(at.UnixNano(), 36) + "|" + globalMember(e)
}

func parseGlobalMember(member string, score float64) (protocol.Entry, error) {
	hex, name, ok := strings.Cut(member, "|")
	rgb, err := strconv.ParseUint(hex, 16, 24)
	if !ok || err != nil || len(hex) != 6 {
		return protocol.Entry{}, fmt.Errorf("%w %q", errBadMember, member)
	}
	return protocol.Entry{
		Name:  name,
		Value: uint32(score),
		Color: protocol.RGB{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb)}, //nolint:gosec // byte extraction from a 24-bit value
	}, nil
}

func parseHourMember(member string, score float64) (protocol.Entry, error) {
	_, rest, ok := strings.Cut(member, "|")
	if !ok {
		return protocol.Entry{}, fmt.Errorf("%w %q", errBadMember, member)
	}
	return parseGlobalMember(rest, score)
}

// RecordScore implements Store.
func (v *Valkey) RecordScore(ctx context.Context, page uint32, board Board, entry protocol.Entry, at time.Time) error {
	if err := v.trimHour(ctx, page, board, at); err != nil {
		return err
	}
	b := v.c.B()
	key, member, score := hourKey(page, board), hourMember(entry, at), float64(entry.Value)
	ttl := int64((2 * HourWindow) / time.Millisecond)
	err := v.tx(ctx,
		b.Zadd().Key(key).ScoreMember().ScoreMember(score, member).Build(),
		b.Zadd().Key(key+":at").ScoreMember().ScoreMember(float64(at.UnixMilli()), member).Build(),
		b.Pexpire().Key(key).Milliseconds(ttl).Build(),
		b.Pexpire().Key(key+":at").Milliseconds(ttl).Build(),
		b.Zadd().Key(globalKey(board)).Gt().ScoreMember().ScoreMember(score, globalMember(entry)).Build(),
		b.Zremrangebyrank().Key(globalKey(board)).Start(0).Stop(-GlobalKeep-1).Build(),
	)
	if err != nil {
		return fmt.Errorf("store: record %s score: %w", board, err)
	}
	return nil
}

// trimHour removes lives that ended more than an hour before now.
func (v *Valkey) trimHour(ctx context.Context, page uint32, board Board, now time.Time) error {
	key := hourKey(page, board)
	cutoff := strconv.FormatInt(now.Add(-HourWindow).UnixMilli(), 10)
	old, err := v.c.Do(ctx, v.c.B().Zrange().Key(key+":at").Min("-inf").Max(cutoff).Byscore().Build()).AsStrSlice()
	if err != nil {
		return fmt.Errorf("store: find expired %s scores: %w", board, err)
	}
	if len(old) == 0 {
		return nil
	}
	err = v.tx(ctx,
		v.c.B().Zrem().Key(key).Member(old...).Build(),
		v.c.B().Zrem().Key(key+":at").Member(old...).Build(),
	)
	if err != nil {
		return fmt.Errorf("store: trim %s scores: %w", board, err)
	}
	return nil
}

// Top implements Store.
func (v *Valkey) Top(ctx context.Context, page uint32, n int, now time.Time) (Tops, error) {
	var tops Tops
	// Fetch extra rows so that de-duplicating names still leaves n.
	fetch := int64(n*5 - 1)
	for board := range NumBoards {
		if err := v.trimHour(ctx, page, board, now); err != nil {
			return Tops{}, err
		}
		hour, err := v.top(ctx, hourKey(page, board), fetch, parseHourMember)
		if err != nil {
			return Tops{}, err
		}
		global, err := v.top(ctx, globalKey(board), fetch, parseGlobalMember)
		if err != nil {
			return Tops{}, err
		}
		tops.Hour[board], tops.Global[board] = bestPerName(hour, n), bestPerName(global, n)
	}
	return tops, nil
}

func (v *Valkey) top(ctx context.Context, key string, stop int64, parse func(string, float64) (protocol.Entry, error)) ([]protocol.Entry, error) {
	cmd := v.c.B().Zrange().Key(key).Min("0").Max(strconv.FormatInt(stop, 10)).Rev().Withscores().Build()
	rows, err := v.c.Do(ctx, cmd).AsZScores()
	if err != nil {
		return nil, fmt.Errorf("store: read %s: %w", key, err)
	}
	entries := make([]protocol.Entry, 0, len(rows))
	for _, row := range rows {
		e, err := parse(row.Member, row.Score)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// SaveSnapshot implements Store.
func (v *Valkey) SaveSnapshot(ctx context.Context, page uint32, data []byte) error {
	if err := v.c.Do(ctx, v.c.B().Set().Key(snapKey(page)).Value(valkey.BinaryString(data)).Px(SnapshotTTL).Build()).Error(); err != nil {
		return fmt.Errorf("store: save snapshot of page %d: %w", page, err)
	}
	return nil
}

// LoadSnapshot implements Store.
func (v *Valkey) LoadSnapshot(ctx context.Context, page uint32) ([]byte, error) {
	data, err := v.c.Do(ctx, v.c.B().Get().Key(snapKey(page)).Build()).AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: load snapshot of page %d: %w", page, err)
	}
	return data, nil
}

// Close implements Store.
func (v *Valkey) Close() error {
	v.c.Close()
	return nil
}
