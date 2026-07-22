package configcenter

import "testing"

func TestCipherRoundTrip(t *testing.T) {
	t.Parallel()
	c, err := NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := c.Encrypt("secret", "wechat.secret")
	if err != nil {
		t.Fatal(err)
	}
	if encoded == "secret" {
		t.Fatal("ciphertext contains plaintext")
	}
	plain, err := c.Decrypt(encoded, "wechat.secret")
	if err != nil {
		t.Fatal(err)
	}
	if plain != "secret" {
		t.Fatalf("got %q", plain)
	}
	if _, err = c.Decrypt(encoded, "other.key"); err == nil {
		t.Fatal("expected AAD mismatch")
	}
}
func TestNewCipherRejectsInvalidKey(t *testing.T) {
	t.Parallel()
	if _, err := NewCipher([]byte("short")); err == nil {
		t.Fatal("expected invalid key error")
	}
}
