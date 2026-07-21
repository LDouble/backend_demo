//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	activityapplication "github.com/weouc-plus/campus-platform/internal/modules/activity/application"
	activitydomain "github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
	activityinfrastructure "github.com/weouc-plus/campus-platform/internal/modules/activity/infrastructure"
)

func TestActivityHTTPFlow(t *testing.T) {
	base, adminToken := integrationAdmin(t)
	client := http.Client{Timeout: 10 * time.Second}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := createIntegrationUser(t, client, base, adminToken, "activity_owner_"+suffix)
	participant := createIntegrationUser(t, client, base, adminToken, "activity_participant_"+suffix)
	bystander := createIntegrationUser(t, client, base, adminToken, "activity_bystander_"+suffix)
	roleID := grantActivityPermissions(t, client, base, adminToken, suffix, owner.id, participant.id, bystander.id)
	_ = roleID

	now := time.Now().UTC()
	startAt := now.Add(2 * time.Hour)
	created := activityResource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/admin/activities", owner.token, map[string]any{
		"title":           "接口活动" + suffix,
		"summary":         "活动搜索摘要",
		"body":            "活动正文内容",
		"location":        "活动中心 A 区",
		"signup_start_at": now.Add(-time.Hour).Format(time.RFC3339),
		"signup_end_at":   now.Add(time.Hour).Format(time.RFC3339),
		"start_at":        startAt.Format(time.RFC3339),
		"end_at":          startAt.Add(2 * time.Hour).Format(time.RFC3339),
		"capacity":        2,
		"contact_type":    "wechat",
		"contact":         "owner_contact_" + suffix,
	}), &created)
	if created.Contact != "owner_contact_"+suffix {
		t.Fatalf("owner contact = %q", created.Contact)
	}

	assertStatus(t, request(t, client, http.MethodGet, base+"/api/v1/admin/activities?keyword=搜索摘要&status=draft&review_status=draft&page=1&page_size=10", owner.token, nil), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/submit-review", base, created.ID), owner.token, map[string]any{
		"expected_version": created.Version,
	}), http.StatusOK)

	submitted := activityResource{}
	decodeData(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/admin/activities/%d", base, created.ID), owner.token, nil), &submitted)
	if submitted.ReviewStatus != "pending_review" {
		t.Fatalf("review status = %s", submitted.ReviewStatus)
	}

	approved := activityResource{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/approve", base, created.ID), adminToken, map[string]any{
		"expected_version": submitted.Version,
		"review_comment":   "通过",
	}), &approved)
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/publish", base, created.ID), owner.token, map[string]any{
		"expected_version": approved.Version,
	}), &approved)
	if approved.Status != "published" || approved.ReviewStatus != "approved" {
		t.Fatalf("published activity = %+v", approved)
	}

	publicList := pageOf[activityResource]{}
	decodeData(t, request(t, client, http.MethodGet, base+"/api/v1/activities?keyword=A%20%E5%8C%BA&page=1&page_size=10", participant.token, nil), &publicList)
	if len(publicList.Items) != 1 || publicList.Items[0].ID != approved.ID {
		t.Fatalf("public items = %+v", publicList.Items)
	}

	bystanderDetail := activityResource{}
	decodeData(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/activities/%d", base, approved.ID), bystander.token, nil), &bystanderDetail)
	if bystanderDetail.Contact == "owner_contact_"+suffix || bystanderDetail.Contact == "" {
		t.Fatalf("bystander contact = %q", bystanderDetail.Contact)
	}

	registration := activityRegistrationResult{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/activities/%d/registrations", base, approved.ID), participant.token, nil), &registration)
	if registration.Registration.Status != "active" || registration.Activity.RegisteredCount != 1 {
		t.Fatalf("registration = %+v", registration)
	}

	participantDetail := activityResource{}
	decodeData(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/activities/%d", base, approved.ID), participant.token, nil), &participantDetail)
	if participantDetail.Contact != "owner_contact_"+suffix {
		t.Fatalf("participant contact = %q", participantDetail.Contact)
	}

	mine := pageOf[myActivityRegistration]{}
	decodeData(t, request(t, client, http.MethodGet, base+"/api/v1/activities/registrations/mine?page=1&page_size=10", participant.token, nil), &mine)
	if len(mine.Items) != 1 || mine.Items[0].ActivityID != approved.ID {
		t.Fatalf("mine items = %+v", mine.Items)
	}

	cancelled := activityRegistrationResult{}
	decodeData(t, request(t, client, http.MethodDelete, fmt.Sprintf("%s/api/v1/activities/%d/registrations/me", base, approved.ID), participant.token, map[string]any{
		"expected_version": registration.Registration.RegistrationVersion,
	}), &cancelled)
	if cancelled.Registration.Status != "cancelled" || cancelled.Activity.RegisteredCount != 0 {
		t.Fatalf("cancelled = %+v", cancelled)
	}

	afterCancel := activityResource{}
	decodeData(t, request(t, client, http.MethodGet, fmt.Sprintf("%s/api/v1/activities/%d", base, approved.ID), participant.token, nil), &afterCancel)
	if afterCancel.Contact == "owner_contact_"+suffix || afterCancel.Contact == "" {
		t.Fatalf("after cancel contact = %q", afterCancel.Contact)
	}
}

func TestActivityReviewEditAndTerminalMasking(t *testing.T) {
	base, adminToken := integrationAdmin(t)
	client := http.Client{Timeout: 10 * time.Second}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner := createIntegrationUser(t, client, base, adminToken, "activity_editor_"+suffix)
	roleID := grantActivityPermissions(t, client, base, adminToken, suffix, owner.id)
	_ = roleID

	now := time.Now().UTC()
	startAt := now.Add(4 * time.Hour)
	created := activityResource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/admin/activities", owner.token, map[string]any{
		"title":           "驳回活动" + suffix,
		"summary":         "需要修改",
		"body":            "初始正文",
		"location":        "图书馆报告厅",
		"signup_start_at": now.Add(-2 * time.Hour).Format(time.RFC3339),
		"signup_end_at":   now.Add(2 * time.Hour).Format(time.RFC3339),
		"start_at":        startAt.Format(time.RFC3339),
		"end_at":          startAt.Add(2 * time.Hour).Format(time.RFC3339),
		"capacity":        5,
		"contact_type":    "qq",
		"contact":         "99887766",
	}), &created)

	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/submit-review", base, created.ID), owner.token, map[string]any{
		"expected_version": created.Version,
	}), &created)
	rejected := activityResource{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/reject", base, created.ID), adminToken, map[string]any{
		"expected_version": created.Version,
		"review_comment":   "补充议程",
	}), &rejected)
	if rejected.ReviewStatus != "rejected" || rejected.ReviewComment == nil || *rejected.ReviewComment == "" {
		t.Fatalf("rejected = %+v", rejected)
	}

	updated := activityResource{}
	decodeData(t, request(t, client, http.MethodPatch, fmt.Sprintf("%s/api/v1/admin/activities/%d", base, created.ID), owner.token, map[string]any{
		"title":            "驳回活动已修订" + suffix,
		"summary":          "修订后摘要",
		"body":             "修订后正文",
		"location":         "体育馆附楼",
		"signup_start_at":  now.Add(-2 * time.Hour).Format(time.RFC3339),
		"signup_end_at":    now.Add(2 * time.Hour).Format(time.RFC3339),
		"start_at":         startAt.Format(time.RFC3339),
		"end_at":           startAt.Add(3 * time.Hour).Format(time.RFC3339),
		"capacity":         6,
		"contact_type":     "qq",
		"contact":          "88776655",
		"expected_version": rejected.Version,
	}), &updated)
	if updated.ReviewStatus != "draft" || updated.ReviewComment != nil {
		t.Fatalf("updated = %+v", updated)
	}

	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/submit-review", base, created.ID), owner.token, map[string]any{
		"expected_version": updated.Version,
	}), &updated)
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/approve", base, created.ID), adminToken, map[string]any{
		"expected_version": updated.Version,
	}), &updated)
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/publish", base, created.ID), owner.token, map[string]any{
		"expected_version": updated.Version,
	}), &updated)
	finished := activityResource{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/admin/activities/%d/finish", base, created.ID), owner.token, map[string]any{
		"expected_version": updated.Version,
	}), &finished)
	if finished.Status != "finished" || finished.Contact == "88776655" {
		t.Fatalf("finished = %+v", finished)
	}
}

func TestActivityConcurrentRegistrationsRespectCapacity(t *testing.T) {
	db := integrationGORM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	activities := activityapplication.NewManager(activityinfrastructure.NewStore(db, integrationContactCipher(t)))
	now := time.Now().UTC()
	startAt := now.Add(3 * time.Hour)
	activity, err := activities.Create(ctx, 91_001, activitydomain.ActivityInput{
		Title:         "并发活动",
		Summary:       "真实 MySQL 报名锁测试",
		Body:          "验证 registered_count 不超过 capacity",
		Location:      "综合楼 101",
		SignupStartAt: now.Add(-time.Hour),
		SignupEndAt:   now.Add(time.Hour),
		StartAt:       startAt,
		EndAt:         startAt.Add(2 * time.Hour),
		Capacity:      1,
		Contact:       activitydomain.ContactInput{Type: "phone", Value: "13800138000", Provided: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	activity, err = activities.SubmitReview(ctx, activity.ID, activity.CreatedBy, activity.Version)
	if err != nil {
		t.Fatal(err)
	}
	activity, err = activities.Approve(ctx, activity.ID, 91_099, activity.Version, "ok")
	if err != nil {
		t.Fatal(err)
	}
	activity, err = activities.Publish(ctx, activity.ID, activity.CreatedBy, activity.Version)
	if err != nil {
		t.Fatal(err)
	}

	var successes int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, _, registerErr := activities.Register(ctx, activity.ID, uint64(91_010+i), fmt.Sprintf("register-%d-%d", activity.ID, i)); registerErr == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("registration successes=%d want=1", successes)
	}

	latest, err := activities.GetAdmin(ctx, activity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.RegisteredCount != 1 {
		t.Fatalf("registered_count=%d want=1", latest.RegisteredCount)
	}
}

type activityResource struct {
	ID              uint64  `json:"id"`
	Version         uint64  `json:"version"`
	Status          string  `json:"status"`
	ReviewStatus    string  `json:"review_status"`
	ReviewComment   *string `json:"review_comment"`
	Contact         string  `json:"contact"`
	RegisteredCount int64   `json:"registered_count"`
}

type pageOf[T any] struct {
	Items []T `json:"items"`
}

type activityRegistrationResult struct {
	Registration struct {
		Status              string `json:"status"`
		RegistrationVersion uint64 `json:"registration_version"`
	} `json:"registration"`
	Activity activityResource `json:"activity"`
}

type myActivityRegistration struct {
	ActivityID uint64           `json:"activity_id"`
	Activity   activityResource `json:"activity"`
}

func grantActivityPermissions(t *testing.T, client http.Client, base, adminToken, suffix string, userIDs ...uint64) uint64 {
	t.Helper()
	role := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/roles", adminToken, map[string]any{
		"name":        "activity_" + suffix,
		"description": "activity 集成测试角色",
	}), &role)
	permissions := []map[string]any{
		{"path_pattern": "/api/v1/admin/activities", "methods": []string{"GET", "POST"}},
		{"path_pattern": "/api/v1/admin/activities/:id", "methods": []string{"GET", "PATCH"}},
		{"path_pattern": "/api/v1/admin/activities/:id/submit-review", "methods": []string{"POST"}},
		{"path_pattern": "/api/v1/admin/activities/:id/publish", "methods": []string{"POST"}},
		{"path_pattern": "/api/v1/admin/activities/:id/cancel", "methods": []string{"POST"}},
		{"path_pattern": "/api/v1/admin/activities/:id/finish", "methods": []string{"POST"}},
		{"path_pattern": "/api/v1/activities", "methods": []string{"GET"}},
		{"path_pattern": "/api/v1/activities/:id", "methods": []string{"GET"}},
		{"path_pattern": "/api/v1/activities/:id/registrations", "methods": []string{"POST"}},
		{"path_pattern": "/api/v1/activities/:id/registrations/me", "methods": []string{"DELETE"}},
		{"path_pattern": "/api/v1/activities/registrations/mine", "methods": []string{"GET"}},
	}
	assertStatus(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/roles/%d/permissions", base, role.ID), adminToken, map[string]any{
		"permissions": permissions,
	}), http.StatusOK)
	for _, id := range userIDs {
		assertStatus(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/users/%d/roles", base, id), adminToken, map[string]any{
			"roles": []string{"activity_" + suffix},
		}), http.StatusOK)
	}
	return role.ID
}
