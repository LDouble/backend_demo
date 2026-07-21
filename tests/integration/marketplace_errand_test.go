//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/application"
	erraddomain "github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
	errandinfrastructure "github.com/weouc-plus/campus-platform/internal/modules/errand/infrastructure"
	marketplaceapplication "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	marketplaceinfrastructure "github.com/weouc-plus/campus-platform/internal/modules/marketplace/infrastructure"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMarketplaceAndErrandHTTPFlows(t *testing.T) {
	base, adminToken := integrationAdmin(t)
	client := http.Client{Timeout: 10 * time.Second}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	requester := createIntegrationUser(t, client, base, adminToken, "requester_"+suffix)
	runner := createIntegrationUser(t, client, base, adminToken, "runner_"+suffix)
	roleID := grantMarketplaceErrandPermissions(t, client, base, adminToken, suffix, requester.id, runner.id)
	_ = roleID

	listing := struct{ ID, Version uint64 }{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/marketplace/listings", requester.token, map[string]any{
		"title": "接口测试教材", "description": "九成新", "price_cents": 1200, "contact_type": "phone", "contact": "13800138000", "image_urls": []string{"https://example.test/book.jpg"},
	}), &listing)
	assertStatus(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/marketplace/listings/%d/submit", base, listing.ID), requester.token, map[string]any{"expected_version": listing.Version}), http.StatusOK)
	assertStatus(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/marketplace/listings/%d/withdraw", base, listing.ID), requester.token, map[string]any{"expected_version": listing.Version + 1}), http.StatusOK)

	task := createErrandThroughAPI(t, client, base, requester.token, suffix)
	accepted := acceptErrandThroughAPI(t, client, base, runner.token, task.ID, task.Version, "accept-"+suffix)
	replayed := acceptErrandThroughAPI(t, client, base, runner.token, task.ID, task.Version, "accept-"+suffix)
	if replayed.Order.ID != accepted.Order.ID {
		t.Fatalf("idempotency replay order=%d want=%d", replayed.Order.ID, accepted.Order.ID)
	}
	pickedUp := errandTask{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/errands/%d/pickup", base, task.ID), runner.token, map[string]any{"expected_version": accepted.Errand.Version}), &pickedUp)
	delivered := errandTask{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/errands/%d/deliver", base, task.ID), runner.token, map[string]any{"expected_version": pickedUp.Version}), &delivered)
	completed := acceptedErrand{}
	decodeData(t, request(t, client, http.MethodPost, fmt.Sprintf("%s/api/v1/errands/%d/complete", base, task.ID), requester.token, map[string]any{"expected_version": delivered.Version}), &completed)
	if completed.Errand.Status != erraddomain.TaskCompleted || completed.Order.TradeStatus != "completed" {
		t.Fatalf("completion result=%+v", completed)
	}
}

func TestMarketplaceAndErrandConcurrentReservations(t *testing.T) {
	db := integrationGORM(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cipher := integrationContactCipher(t)
	marketplace := marketplaceapplication.NewManager(marketplaceinfrastructure.NewStore(db, cipher))
	listing, err := marketplace.Create(ctx, 80_001, marketplacedomain.ListingInput{Title: "并发商品", Description: "真实 MySQL 锁测试", PriceCents: 100, ImageURLs: []string{"https://example.test/item.jpg"}, Contact: marketplacedomain.ContactInput{Type: "phone", Value: "13800138000", Provided: true}})
	if err != nil {
		t.Fatal(err)
	}
	listing, err = marketplace.Submit(ctx, listing.ID, listing.OwnerId, listing.Version)
	if err != nil {
		t.Fatal(err)
	}
	listing, err = marketplace.Review(ctx, listing.ID, 80_099, listing.Version, true, "")
	if err != nil {
		t.Fatal(err)
	}
	var reservationSuccesses int
	var reservationMu sync.Mutex
	var reservationWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		reservationWG.Add(1)
		go func(i int) {
			defer reservationWG.Done()
			if _, reserveErr := marketplace.Reserve(ctx, listing.ID, uint64(80_010+i), fmt.Sprintf("listing-%d-%d", listing.ID, i)); reserveErr == nil {
				reservationMu.Lock()
				reservationSuccesses++
				reservationMu.Unlock()
			}
		}(i)
	}
	reservationWG.Wait()
	if reservationSuccesses != 1 {
		t.Fatalf("marketplace reservation successes=%d want=1", reservationSuccesses)
	}

	errand := application.NewManager(errandinfrastructure.NewStore(db, cipher))
	task, err := errand.Create(ctx, 81_001, erraddomain.TaskInput{Title: "并发跑腿", Description: "真实 MySQL 锁测试", RewardCents: 200, PickupLocation: "东门", DropoffLocation: "图书馆", Deadline: time.Now().Add(time.Hour), Contact: erraddomain.ContactInput{Type: "phone", Value: "13800138000", Provided: true}})
	if err != nil {
		t.Fatal(err)
	}
	var acceptSuccesses int
	var acceptMu sync.Mutex
	var acceptWG sync.WaitGroup
	for i := 0; i < 2; i++ {
		acceptWG.Add(1)
		go func(i int) {
			defer acceptWG.Done()
			if _, _, acceptErr := errand.Accept(ctx, task.ID, uint64(81_010+i), task.Version, fmt.Sprintf("errand-%d-%d", task.ID, i)); acceptErr == nil {
				acceptMu.Lock()
				acceptSuccesses++
				acceptMu.Unlock()
			}
		}(i)
	}
	acceptWG.Wait()
	if acceptSuccesses != 1 {
		t.Fatalf("errand accept successes=%d want=1", acceptSuccesses)
	}
}

type integrationUser struct {
	id    uint64
	token string
}
type errandTask struct {
	ID      uint64 `json:"id"`
	Version uint64 `json:"version"`
	Status  string `json:"status"`
}
type tradeSummary struct {
	ID          uint64 `json:"id"`
	TradeStatus string `json:"trade_status"`
}
type acceptedErrand struct {
	Errand errandTask   `json:"errand"`
	Order  tradeSummary `json:"order"`
}

func createIntegrationUser(t *testing.T, client http.Client, base, adminToken, username string) integrationUser {
	t.Helper()
	created := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/users", adminToken, map[string]any{"username": username, "password": "integration-password"}), &created)
	return integrationUser{id: created.ID, token: loginWithCredentials(t, base, username, "integration-password")}
}
func grantMarketplaceErrandPermissions(t *testing.T, client http.Client, base, adminToken, suffix string, userIDs ...uint64) uint64 {
	t.Helper()
	role := resource{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/roles", adminToken, map[string]any{"name": "tradeflow_" + suffix, "description": "交易流程接口测试"}), &role)
	permissions := []map[string]any{{"path_pattern": "/api/v1/marketplace/listings", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/marketplace/listings/:id/submit", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/marketplace/listings/:id/withdraw", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/errands", "methods": []string{"GET", "POST"}}, {"path_pattern": "/api/v1/errands/mine", "methods": []string{"GET"}}, {"path_pattern": "/api/v1/errands/:id", "methods": []string{"GET", "PATCH"}}, {"path_pattern": "/api/v1/errands/:id/accept", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/errands/:id/pickup", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/errands/:id/deliver", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/errands/:id/complete", "methods": []string{"POST"}}, {"path_pattern": "/api/v1/errands/:id/cancel", "methods": []string{"POST"}}}
	assertStatus(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/roles/%d/permissions", base, role.ID), adminToken, map[string]any{"permissions": permissions}), http.StatusOK)
	for _, id := range userIDs {
		assertStatus(t, request(t, client, http.MethodPut, fmt.Sprintf("%s/api/v1/users/%d/roles", base, id), adminToken, map[string]any{"roles": []string{"tradeflow_" + suffix}}), http.StatusOK)
	}
	return role.ID
}
func createErrandThroughAPI(t *testing.T, client http.Client, base, token, suffix string) errandTask {
	t.Helper()
	task := errandTask{}
	decodeData(t, request(t, client, http.MethodPost, base+"/api/v1/errands", token, map[string]any{"title": "接口跑腿" + suffix, "description": "请帮忙取件", "reward_cents": 300, "pickup_location": "快递站", "dropoff_location": "宿舍", "deadline": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "contact_type": "wechat", "contact": "requester_" + suffix}), &task)
	return task
}

func integrationContactCipher(t *testing.T) *configcenter.Cipher {
	t.Helper()
	key := sha256.Sum256([]byte("integration restricted contact cipher"))
	cipher, err := configcenter.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}
func acceptErrandThroughAPI(t *testing.T, client http.Client, base, token string, id, version uint64, key string) acceptedErrand {
	t.Helper()
	body, err := json.Marshal(map[string]any{"expected_version": version})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/errands/%d/accept", base, id), bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", key)
	req.Header.Set("Content-Type", "application/json")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	result := acceptedErrand{}
	decodeData(t, response, &result)
	return result
}
func integrationGORM(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_DSN")
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	return db
}
