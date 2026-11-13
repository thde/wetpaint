package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/thde/wetpaint/server/internal/store"
)

// defaultAddr is the listen address used when -addr is not given: $PORT if
// the platform sets it (Deploio, Heroku-style hosts), else :8080.
func defaultAddr(getenv func(string) string) string {
	if port := getenv("PORT"); port != "" {
		return ":" + port
	}
	return ":8080"
}

// defaultStatic is the client directory used when -static is not given: ko
// images ship the client as kodata and set $KO_DATA_PATH to its location.
func defaultStatic(getenv func(string) string) string {
	return getenv("KO_DATA_PATH")
}

// Deploio injects one set of variables per attached Key-Value Store,
// NINE_KVS_<NAME>_<KEY>, where <NAME> is the service reference name. See
// https://docs.nine.ch/docs/deplo-io/configuration/deploio-connecting-to-services.md
const (
	kvsPrefix      = "NINE_KVS_"
	kvsFQDNSuffix  = "_FQDN"
	kvsDefaultPort = "6379"
)

var (
	errSeveralKVS = errors.New("found several Key-Value Stores in the environment")
	errNoPEM      = errors.New("holds no PEM certificate")
)

// kvsFromEnv finds a Deploio Key-Value Store in environ (KEY=VALUE pairs). It
// returns ok=false if none is attached and an error if several are, since
// there is no way to tell which one is meant.
func kvsFromEnv(environ []string) (opts store.ValkeyOptions, name string, ok bool, err error) {
	env := make(map[string]string, len(environ))
	var names []string
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
		if n, found := strings.CutPrefix(k, kvsPrefix); found {
			if n, found = strings.CutSuffix(n, kvsFQDNSuffix); found && n != "" && v != "" {
				names = append(names, n)
			}
		}
	}
	switch len(names) {
	case 0:
		return store.ValkeyOptions{}, "", false, nil
	case 1:
	default:
		slices.Sort(names)
		return store.ValkeyOptions{}, "", false, fmt.Errorf(
			"%w (%s); pass -valkey to choose", errSeveralKVS,
			strings.Join(names, ", "))
	}

	name = names[0]
	get := func(key string) string { return env[kvsPrefix+name+"_"+key] }
	host, port := get("FQDN"), get("PORT")
	if port == "" {
		port = kvsDefaultPort
	}
	// Deploio's DSN for these is rediss://, so the connection is always TLS.
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if pem := get("CA_CERT"); pem != "" {
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(pem)) {
			return store.ValkeyOptions{}, "", false, fmt.Errorf("%s%s_CA_CERT %w", kvsPrefix, name, errNoPEM)
		}
		tlsCfg.RootCAs = pool
	}
	return store.ValkeyOptions{
		Addr:     net.JoinHostPort(host, port),
		Username: get("USER"),
		Password: get("PASSWORD"),
		TLS:      tlsCfg,
	}, name, true, nil
}
