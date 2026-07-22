package infrastructure

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
	tradedomain "github.com/weouc-plus/campus-platform/internal/modules/trade/domain"
	"gorm.io/gorm"
)

func TestListingQueriesEnforceVisibilityAndReturnImages(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&domain.Listing{}, &domain.ListingImage{}, &tradedomain.Order{}); err != nil {
		t.Fatal(err)
	}
	cipher, err := configcenter.NewCipher([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, cipher)
	published := createQueryListing(t, db, store, 1, domain.ListingPublished, "Published bike", 1000)
	draft := createQueryListing(t, db, store, 1, domain.ListingDraft, "Draft lamp", 2000)
	if err = db.Create(&domain.ListingImage{ListingId: published.ID, Url: "https://example.com/1.jpg", Position: 0}).Error; err != nil {
		t.Fatal(err)
	}

	minPrice := int64(900)
	maxPrice := int64(1100)
	rows, total, err := store.ListPublished(context.Background(), domain.ListingSearch{
		Keyword: "bike", MinPriceCents: &minPrice, MaxPriceCents: &maxPrice, Page: 1, PageSize: 20,
	})
	if err != nil || total != 1 || len(rows) != 1 || len(rows[0].ImageURLs) != 1 {
		t.Fatalf("rows=%#v total=%d err=%v", rows, total, err)
	}
	if _, err = store.GetVisible(context.Background(), draft.ID, 2); statusOf(err) != 404 {
		t.Fatalf("stranger draft error = %v", err)
	}
	if _, err = store.GetVisible(context.Background(), draft.ID, 1); err != nil {
		t.Fatalf("owner draft error = %v", err)
	}

	draft.Status = domain.ListingReserved
	if err = db.Save(draft).Error; err != nil {
		t.Fatal(err)
	}
	order := tradedomain.Order{
		OrderNo: "TRDQUERY", OrderType: tradedomain.OrderTypeMarketplace,
		ResourceType: tradedomain.ResourceListing, ResourceId: draft.ID,
		BuyerId: 2, SellerId: 1, AmountCents: draft.PriceCents, Currency: domain.CurrencyCNY,
		PaymentMode: tradedomain.PaymentOffline, TradeStatus: tradedomain.StatusConfirmed,
		FulfillmentStatus: tradedomain.FulfillmentNotStarted, TitleSnapshot: draft.Title,
		ResourceSnapshot: []byte(`{}`), IdempotencyKey: "query-test", Version: 1,
	}
	if err = db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	details, err := store.GetVisible(context.Background(), draft.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	contact, err := store.Contact(context.Background(), &details.Listing, 2)
	if err != nil || contact.Value != "13800138000" {
		t.Fatalf("contact=%#v err=%v", contact, err)
	}
}

func statusOf(err error) int {
	value, ok := apperror.As(err)
	if !ok {
		return 0
	}
	return value.Status
}

func createQueryListing(
	t *testing.T,
	db *gorm.DB,
	store *Store,
	ownerID uint64,
	status string,
	title string,
	price int64,
) *domain.Listing {
	t.Helper()
	listing := &domain.Listing{
		Title: title, Description: title + " description", PriceCents: price,
		Currency: domain.CurrencyCNY, Status: status, OwnerId: ownerID,
		ContactType: "phone", Version: 1,
	}
	if err := db.Create(listing).Error; err != nil {
		t.Fatal(err)
	}
	ciphertext, err := store.encryptContact("13800138000", listingContactAAD(listing.ID))
	if err != nil {
		t.Fatal(err)
	}
	listing.ContactCiphertext = ciphertext
	if err = db.Save(listing).Error; err != nil {
		t.Fatal(err)
	}
	return listing
}
