package generator

import (
	"context"
	"path/filepath"
	"testing"
)

func TestPublishingSchemasRequireAcademicVerification(t *testing.T) {
	tests := []struct {
		name     string
		schema   string
		required []string
		admin    []string
	}{
		{
			name:     "activity",
			schema:   "activity.yaml",
			required: []string{"CreateActivity", "SubmitActivityReview"},
			admin:    []string{"CreateAdminActivity", "SubmitAdminActivityReview", "ApproveAdminActivity", "RejectAdminActivity"},
		},
		{
			name:     "marketplace",
			schema:   "marketplace.yaml",
			required: []string{"CreateMarketplaceListing", "SubmitMarketplaceListing"},
			admin:    []string{"ReviewMarketplaceListing", "RemoveMarketplaceListing"},
		},
		{
			name:     "errand",
			schema:   "errand.yaml",
			required: []string{"CreateErrand", "SubmitErrandReview"},
			admin:    []string{"ReviewErrand", "RevokeErrandReview"},
		},
		{
			name:     "carpool",
			schema:   "carpool.yaml",
			required: []string{"CreateCarpoolTrip", "SubmitCarpoolTripReview"},
			admin:    []string{"ReviewCarpoolTrip", "RevokeCarpoolTripReview"},
		},
		{
			name:     "comment",
			schema:   "comment.yaml",
			required: []string{"CreateComment", "SubmitCommentReview"},
			admin:    []string{"ReviewComment", "RevokeCommentReview"},
		},
		{
			name:     "campus circle",
			schema:   "campus_circle.yaml",
			required: []string{"CreateCampusCirclePost", "SubmitCampusCirclePostReview"},
			admin:    []string{"ReviewCampusCirclePost", "RevokeCampusCirclePostReview"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema, err := Load(
				context.Background(),
				filepath.Join("..", "..", "schemas", test.schema),
			)
			if err != nil {
				t.Fatal(err)
			}

			operations := make(map[string]APIOperation, len(schema.Operations))
			for _, operation := range schema.Operations {
				operations[operation.OperationID] = operation
			}
			assertAcademicVerification(t, operations, test.required, "required")
			assertAcademicVerification(t, operations, test.admin, "none")
		})
	}
}

func assertAcademicVerification(
	t *testing.T,
	operations map[string]APIOperation,
	operationIDs []string,
	want string,
) {
	t.Helper()
	for _, operationID := range operationIDs {
		operation, ok := operations[operationID]
		if !ok {
			t.Errorf("operation %q is not declared", operationID)
			continue
		}
		if operation.AcademicVerification != want {
			t.Errorf(
				"operation %q academic_verification = %q, want %q",
				operationID,
				operation.AcademicVerification,
				want,
			)
		}
	}
}
