package redisclient

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
)

func TestTLSConfigUsesScopedRegularFiles(t *testing.T) {
	root := t.TempDir()
	certificatePEM, keyPEM := testCertificate(t)
	if err := os.WriteFile(filepath.Join(root, "ca.pem"), certificatePEM, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "client.pem"), certificatePEM, 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "client.key")
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	config := bootstrap.RedisConfig{
		TLS:            true,
		TLSFilesRoot:   root,
		CAFile:         "ca.pem",
		ClientCertFile: "client.pem",
		ClientKeyFile:  "client.key",
	}
	tlsConfig, err := TLSConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if tlsConfig.RootCAs == nil || len(tlsConfig.Certificates) != 1 {
		t.Fatalf("TLS config=%+v", tlsConfig)
	}
	if err = os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = TLSConfig(config); err == nil {
		t.Fatal("world-readable client key was accepted")
	}
}

func TestTLSConfigRejectsPathAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "ca.pem")
	certificatePEM, _ := testCertificate(t)
	if err := os.WriteFile(external, certificatePEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "linked.pem")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../ca.pem", external, "linked.pem"} {
		if _, err := TLSConfig(bootstrap.RedisConfig{TLS: true, TLSFilesRoot: root, CAFile: name}); err == nil {
			t.Fatalf("unsafe CA path %q was accepted", name)
		}
	}
}

func testCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "redis.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}
