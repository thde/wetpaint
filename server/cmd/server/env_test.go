package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestDefaultAddr(t *testing.T) {
	t.Parallel()
	env := func(port string) func(string) string {
		return func(k string) string {
			if k == "PORT" {
				return port
			}
			return ""
		}
	}
	if got := defaultAddr(env("")); got != ":8080" {
		t.Errorf("without PORT: got %q, want :8080", got)
	}
	if got := defaultAddr(env("3000")); got != ":3000" {
		t.Errorf("with PORT=3000: got %q, want :3000", got)
	}
}

func TestDefaultStatic(t *testing.T) {
	t.Parallel()
	env := func(dir string) func(string) string {
		return func(k string) string {
			if k == "KO_DATA_PATH" {
				return dir
			}
			return ""
		}
	}
	if got := defaultStatic(env("")); got != "" {
		t.Errorf("without KO_DATA_PATH: got %q, want empty", got)
	}
	if got := defaultStatic(env("/var/run/ko")); got != "/var/run/ko" {
		t.Errorf("with KO_DATA_PATH=/var/run/ko: got %q, want /var/run/ko", got)
	}
}

func TestKVSFromEnvNone(t *testing.T) {
	t.Parallel()
	_, _, ok, err := kvsFromEnv([]string{"PATH=/bin", "NINE_PG_DB_FQDN=pg.example", "NINE_KVS_CACHE_FQDN="})
	if err != nil || ok {
		t.Fatalf("got ok=%v err=%v, want ok=false err=nil", ok, err)
	}
}

func TestKVSFromEnvOne(t *testing.T) {
	t.Parallel()
	opts, name, ok, err := kvsFromEnv([]string{
		"NINE_KVS_MY_CACHE_FQDN=kvs.example.com",
		"NINE_KVS_MY_CACHE_PORT=6380",
		"NINE_KVS_MY_CACHE_USER=default",
		"NINE_KVS_MY_CACHE_PASSWORD=p@ss=word",
		"NINE_KVS_MY_CACHE_DSN=rediss://default:p@ss=word@kvs.example.com:6380",
	})
	if err != nil || !ok {
		t.Fatalf("got ok=%v err=%v", ok, err)
	}
	if name != "MY_CACHE" {
		t.Errorf("name = %q, want MY_CACHE", name)
	}
	if opts.Addr != "kvs.example.com:6380" || opts.Username != "default" || opts.Password != "p@ss=word" {
		t.Errorf("opts = %+v", opts)
	}
	if opts.TLS == nil || opts.TLS.ServerName != "kvs.example.com" {
		t.Errorf("TLS = %+v, want ServerName kvs.example.com", opts.TLS)
	}
	if opts.TLS.RootCAs != nil {
		t.Error("RootCAs set without a CA_CERT; want the system pool (nil)")
	}
}

func TestKVSFromEnvDefaultPort(t *testing.T) {
	t.Parallel()
	opts, _, ok, err := kvsFromEnv([]string{"NINE_KVS_C_FQDN=kvs.example.com"})
	if err != nil || !ok || opts.Addr != "kvs.example.com:6379" {
		t.Fatalf("got %+v ok=%v err=%v, want addr kvs.example.com:6379", opts, ok, err)
	}
}

func TestKVSFromEnvCACert(t *testing.T) {
	t.Parallel()
	ca := testCAPEM(t)
	opts, _, ok, err := kvsFromEnv([]string{"NINE_KVS_C_FQDN=kvs.example.com", "NINE_KVS_C_CA_CERT=" + ca})
	if err != nil || !ok {
		t.Fatalf("got ok=%v err=%v", ok, err)
	}
	if opts.TLS.RootCAs == nil {
		t.Fatal("RootCAs is nil with a CA_CERT")
	}

	_, _, ok, err = kvsFromEnv([]string{"NINE_KVS_C_FQDN=kvs.example.com", "NINE_KVS_C_CA_CERT=not a cert"})
	if ok || err == nil || !strings.Contains(err.Error(), "NINE_KVS_C_CA_CERT") {
		t.Errorf("bad CA_CERT: ok=%v err=%v, want an error naming the variable", ok, err)
	}
}

func TestKVSFromEnvSeveral(t *testing.T) {
	t.Parallel()
	_, _, ok, err := kvsFromEnv([]string{"NINE_KVS_B_FQDN=b.example", "NINE_KVS_A_FQDN=a.example"})
	if err == nil || ok {
		t.Fatalf("got ok=%v err=%v, want an error", ok, err)
	}
	if !strings.Contains(err.Error(), "A, B") {
		t.Errorf("err = %v, want it to list A, B", err)
	}
}

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}
