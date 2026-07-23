package app

import (
	"context"
	"errors"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	campuscircleapp "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	campuscircledomain "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/domain"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
)

type campusCircleCommentTargetStub struct {
	item     campuscircleapp.Item
	err      error
	viewerID uint64
	admin    bool
}

func (s *campusCircleCommentTargetStub) GetPost(
	_ context.Context,
	_ uint64,
	viewerID uint64,
	admin bool,
) (campuscircleapp.Item, error) {
	s.viewerID = viewerID
	s.admin = admin
	return s.item, s.err
}

func TestCommentTargetResolverCampusCirclePost(t *testing.T) {
	targetError := errors.New("target unavailable")
	tests := []struct {
		name      string
		item      campuscircleapp.Item
		targetID  uint64
		err       error
		wantOwner uint64
		wantCode  string
		wantErr   error
	}{
		{
			name: "visible post",
			item: campuscircleapp.Item{Post: campuscircledomain.CampusCirclePost{
				ID: 7, AuthorId: 42,
			}},
			targetID:  7,
			wantOwner: 42,
		},
		{
			name:     "mismatched post",
			item:     campuscircleapp.Item{Post: campuscircledomain.CampusCirclePost{ID: 8}},
			targetID: 7,
			wantCode: "comment_target_not_found",
		},
		{
			name:     "visibility error",
			targetID: 7,
			err:      targetError,
			wantErr:  targetError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &campusCircleCommentTargetStub{item: test.item, err: test.err}
			resolver := commentTargetResolver{campusCircle: stub}
			target, err := resolver.Resolve(
				context.Background(),
				commentapp.TargetCampusCirclePost,
				test.targetID,
				99,
			)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if test.wantCode != "" {
				appError, ok := apperror.As(err)
				if !ok || appError.Code != test.wantCode {
					t.Fatalf("Resolve() error = %v, want code %q", err, test.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if target.OwnerID != test.wantOwner {
				t.Fatalf("owner ID = %d, want %d", target.OwnerID, test.wantOwner)
			}
			if stub.viewerID != 99 || stub.admin {
				t.Fatalf("GetPost() viewer = %d, admin = %t", stub.viewerID, stub.admin)
			}
		})
	}
}
