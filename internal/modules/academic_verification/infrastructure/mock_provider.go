package infrastructure

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/application"
	"golang.org/x/crypto/bcrypt"
)

type mockCredential struct {
	StudentNo    string `json:"student_no"`
	RealName     string `json:"real_name"`
	PasswordHash string `json:"password_hash"`
}

// MockProvider verifies a Secret-mounted bcrypt whitelist.
type MockProvider struct {
	entries   map[string]mockCredential
	dummyHash []byte
}

// NewMockProvider loads the operator-controlled whitelist without logging its contents.
func NewMockProvider(path string) (*MockProvider, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("academic provider whitelist path is required")
	}
	// #nosec G304 -- this is an explicit operator-controlled Secret path, never request input.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read academic provider whitelist: %w", err)
	}
	entries := []mockCredential{}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode academic provider whitelist: %w", err)
	}
	byStudent := make(map[string]mockCredential, len(entries))
	for _, entry := range entries {
		entry.StudentNo = strings.TrimSpace(entry.StudentNo)
		entry.RealName = strings.TrimSpace(entry.RealName)
		if entry.StudentNo == "" || entry.RealName == "" || entry.PasswordHash == "" {
			return nil, fmt.Errorf("academic provider whitelist contains an incomplete entry")
		}
		if _, exists := byStudent[entry.StudentNo]; exists {
			return nil, fmt.Errorf("academic provider whitelist contains a duplicate student number")
		}
		if _, err = bcrypt.Cost([]byte(entry.PasswordHash)); err != nil {
			return nil, fmt.Errorf("academic provider whitelist contains an invalid password hash")
		}
		byStudent[entry.StudentNo] = entry
	}
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("academic-provider-timing-placeholder"), 12)
	if err != nil {
		return nil, fmt.Errorf("create academic provider placeholder: %w", err)
	}
	return &MockProvider{entries: byStudent, dummyHash: dummyHash}, nil
}

// Verify performs bcrypt for both known and unknown student numbers.
func (p *MockProvider) Verify(ctx context.Context, studentNo, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", application.ErrProviderUnavailable
	}
	entry, exists := p.entries[strings.TrimSpace(studentNo)]
	hash := p.dummyHash
	if exists {
		hash = []byte(entry.PasswordHash)
	}
	err := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if err != nil || !exists {
		return "", application.ErrInvalidCredentials
	}
	if err = ctx.Err(); err != nil {
		return "", application.ErrProviderUnavailable
	}
	return entry.RealName, nil
}
