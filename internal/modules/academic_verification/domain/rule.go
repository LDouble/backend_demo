// Package domain defines academic-verification state and persistence entities.
package domain

import "time"

const (
	// MethodStudentCard identifies manual student-card review.
	MethodStudentCard = "student_card"
	// MethodCredentials identifies synchronous provider verification.
	MethodCredentials = "credentials"

	// IdentityVerified grants the member base role.
	IdentityVerified = "verified"
	// IdentityRevoked removes the member base role.
	IdentityRevoked = "revoked"

	// RequestPending awaits an administrator decision.
	RequestPending = "pending"
	// RequestApproved created or replaced an academic identity.
	RequestApproved = "approved"
	// RequestRejected records an administrator rejection.
	RequestRejected = "rejected"
	// RequestSuperseded was made obsolete by another successful verification.
	RequestSuperseded = "superseded"

	// MaterialAvailable is an unbound one-time upload.
	MaterialAvailable = "available"
	// MaterialBound belongs to one verification request.
	MaterialBound = "bound"
	// MaterialDeleting is exclusively claimed by the cleanup worker.
	MaterialDeleting = "deleting"
	// MaterialDeleted retains metadata after encrypted content removal.
	MaterialDeleted = "deleted"
)

const (
	// MaxMaterialBytes is the maximum plaintext student-card size.
	MaxMaterialBytes int64 = 5 << 20
	// UnboundMaterialTTL is the upload-token lifetime.
	UnboundMaterialTTL = 24 * time.Hour
	// ReviewedMaterialRetention is the post-review encrypted material retention.
	ReviewedMaterialRetention = 30 * 24 * time.Hour
	// CleanupClaimLease allows another worker to recover an abandoned deletion claim.
	CleanupClaimLease = 15 * time.Minute
)

// Status is the caller-visible academic verification snapshot.
type Status struct {
	Identity      *AcademicIdentity            `json:"identity"`
	LatestRequest *AcademicVerificationRequest `json:"latest_request"`
}

// MaterialContent is decrypted only for a currently authorized administrator.
type MaterialContent struct {
	MIMEType string
	Data     []byte
}

// VerificationEvent is intentionally free of names, student numbers, credentials and storage metadata.
type VerificationEvent struct {
	UserID     uint64 `json:"user_id"`
	RequestID  uint64 `json:"request_id,omitempty"`
	Status     string `json:"status"`
	ActionPath string `json:"action_path"`
	Version    uint64 `json:"version"`
}

// GetVersion provides stable domain-event deduplication.
func (e VerificationEvent) GetVersion() uint64 { return e.Version }
