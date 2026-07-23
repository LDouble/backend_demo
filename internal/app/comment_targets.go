package app

import (
	"context"
	"net/http"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	activityapp "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	commentapp "github.com/weouc-plus/campus-platform/internal/modules/comment/application"
	errandapp "github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
)

type commentTargetResolver struct {
	activities  *activityapp.Manager
	marketplace *marketplaceapp.Manager
	errands     *errandapp.Manager
	carpools    *carpoolapp.Manager
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
