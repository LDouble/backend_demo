package app

import (
	"context"
	"net/http"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	campuscircleapp "github.com/weouc-plus/campus-platform/internal/modules/campus_circle/application"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
)

type campusCircleCommentTarget interface {
	GetPost(context.Context, uint64, uint64, bool) (campuscircleapp.Item, error)
}

type commentTargetResolver struct {
	activities   *activityapp.Manager
	campusCircle campusCircleCommentTarget
	marketplace  *marketplaceapp.Manager
	errands      *errandapp.Manager
	carpools     *carpoolapp.Manager
}

func (r commentTargetResolver) Resolve(
	ctx context.Context,
	targetType string,
	targetID,
	viewerID uint64,
) (commentapp.Target, error) {
	switch targetType {
	case commentapp.TargetActivity:
		target, err := r.activities.GetPublic(ctx, targetID, viewerID)
		if err != nil {
			return commentapp.Target{}, err
		}
		if target.ID != targetID {
			return commentapp.Target{}, commentTargetNotFound()
		}
		return commentapp.Target{OwnerID: target.CreatedBy}, nil
	case commentapp.TargetMarketplace:
		target, err := r.marketplace.Get(ctx, targetID, viewerID)
		if err != nil {
			return commentapp.Target{}, err
		}
		if target.ID != targetID {
			return commentapp.Target{}, commentTargetNotFound()
		}
		return commentapp.Target{OwnerID: target.OwnerId}, nil
	case commentapp.TargetErrand:
		target, err := r.errands.GetVisible(ctx, targetID, viewerID)
		if err != nil {
			return commentapp.Target{}, err
		}
		if target.ID != targetID {
			return commentapp.Target{}, commentTargetNotFound()
		}
		return commentapp.Target{OwnerID: target.RequesterId}, nil
	case commentapp.TargetCarpool:
		target, _, err := r.carpools.Get(ctx, targetID, viewerID)
		if err != nil {
			return commentapp.Target{}, err
		}
		if target.ID != targetID {
			return commentapp.Target{}, commentTargetNotFound()
		}
		return commentapp.Target{OwnerID: target.OrganizerId}, nil
	case commentapp.TargetCampusCirclePost:
		target, err := r.campusCircle.GetPost(ctx, targetID, viewerID, false)
		if err != nil {
			return commentapp.Target{}, err
		}
		if target.Post.ID != targetID {
			return commentapp.Target{}, commentTargetNotFound()
		}
		return commentapp.Target{OwnerID: target.Post.AuthorId}, nil
	default:
		return commentapp.Target{}, apperror.New(
			http.StatusBadRequest,
			"unsupported_comment_target",
			"该资源类型暂不支持评论",
		)
	}
}

func commentTargetNotFound() error {
	return apperror.New(http.StatusNotFound, "comment_target_not_found", "评论目标不存在")
}
