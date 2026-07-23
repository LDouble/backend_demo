package infrastructure

import (
	"context"
	"errors"
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
	if err = db.Create(&domain.ListingImage{ListingId: draft.ID, Url: "https://example.com/draft.jpg", Position: 0}).Error; err != nil {
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
	updated, err := store.UpdateListing(context.Background(), draft.ID, 1, draft.Version, domain.ListingInput{
		Title: "Updated lamp", Description: "Updated description", PriceCents: 2100,
		ImageURLs: []string{}, ImageURLsProvided: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if count := imageCount(t, db, draft.ID); count != 1 {
		t.Fatalf("omitted images count = %d", count)
	}
	updated, err = store.UpdateListing(context.Background(), draft.ID, 1, updated.Version, domain.ListingInput{
		Title: "Updated lamp", Description: "Updated description", PriceCents: 2100,
		ImageURLs: []string{}, ImageURLsProvided: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if count := imageCount(t, db, draft.ID); count != 0 {
		t.Fatalf("explicit empty images count = %d", count)
	}

	updated.Status = domain.ListingReserved
	if err = db.Save(updated).Error; err != nil {
		t.Fatal(err)
	}
	order := tradedomain.Order{
		OrderNo: "TRDQUERY", OrderType: tradedomain.OrderTypeMarketplace,
		ResourceType: tradedomain.ResourceListing, ResourceId: updated.ID,
		BuyerId: 2, SellerId: 1, AmountCents: updated.PriceCents, Currency: domain.CurrencyCNY,
		PaymentMode: tradedomain.PaymentOffline, TradeStatus: tradedomain.StatusConfirmed,
		FulfillmentStatus: tradedomain.FulfillmentNotStarted, TitleSnapshot: updated.Title,
		ResourceSnapshot: []byte(`{}`), IdempotencyKey: "query-test", Version: 1,
	}
	if err = db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	details, err := store.GetVisible(context.Background(), updated.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	contact, err := store.Contact(context.Background(), &details.Listing, 2)
	if err != nil || contact.Value != "13800138000" {
		t.Fatalf("contact=%#v err=%v", contact, err)
	}
	contacts, err := store.Contacts(context.Background(), []domain.ListingDetails{
		*details,
		{Listing: *published, ImageURLs: []string{"https://example.com/1.jpg"}},
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if contacts[updated.ID].Value != "13800138000" || contacts[published.ID].Value == "13800138000" {
		t.Fatalf("batch contacts = %#v", contacts)
	}
	active, err := store.ActiveBuyerListings(context.Background(), 2, []uint64{updated.ID, published.ID})
	if err != nil || !active[updated.ID] || active[published.ID] {
		t.Fatalf("active buyer listings=%v err=%v", active, err)
	}
	anonymous, err := store.ActiveBuyerListings(context.Background(), 0, []uint64{updated.ID})
	if err != nil || len(anonymous) != 0 {
		t.Fatalf("anonymous active buyer listings=%v err=%v", anonymous, err)
	}
	empty, err := store.ActiveBuyerListings(context.Background(), 2, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty active buyer listings=%v err=%v", empty, err)
	}
	want := errors.New("orders unavailable")
	if err = db.Callback().Query().Before("gorm:query").Register("test:fail-active-orders", func(tx *gorm.DB) {
		_ = tx.AddError(want)
	}); err != nil {
		t.Fatal(err)
	}
	active, err = store.ActiveBuyerListings(context.Background(), 2, []uint64{updated.ID})
	if !errors.Is(err, want) || active != nil {
		t.Fatalf("failed active buyer listings=%v err=%v", active, err)
	}
}

func imageCount(t *testing.T, db *gorm.DB, listingID uint64) int64 {
	t.Helper()
	var count int64
	if err := db.Model(&domain.ListingImage{}).Where("listing_id = ?", listingID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
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
