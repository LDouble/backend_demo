package httpapi

import (
	"testing"

	"github.com/weouc-plus/campus-platform/internal/api/generated"
	marketplacedomain "github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

func TestMarketplaceListingUpdateInputPreservesImageOmission(t *testing.T) {
	base := generated.UpdateMarketplaceListingJSONBody{
		Title: "Lamp", Description: "Desk lamp", PriceCents: 100,
		ExpectedVersion: 1,
	}
	omitted := marketplaceListingUpdateInput(base)
	if omitted.ImageURLsProvided || len(omitted.ImageURLs) != 0 {
		t.Fatalf("omitted images = %#v", omitted)
	}
	empty := []string{}
	base.ImageUrls = &empty
	cleared := marketplaceListingUpdateInput(base)
	if !cleared.ImageURLsProvided || len(cleared.ImageURLs) != 0 {
		t.Fatalf("explicit empty images = %#v", cleared)
	}
}

func TestMarketplaceListingViewIncludesViewerContext(t *testing.T) {
	view := marketplaceListingViewOf(
		marketplacedomain.ListingDetails{Listing: marketplacedomain.Listing{ID: 3}},
		marketplacedomain.ContactDetails{},
		marketplacedomain.ViewerRelationNone,
		[]string{marketplacedomain.ActionPurchase},
	)
	if view.ViewerRelation != marketplacedomain.ViewerRelationNone ||
		len(view.AvailableActions) != 1 ||
		view.AvailableActions[0] != marketplacedomain.ActionPurchase {
		t.Fatalf("view=%+v", view)
	}
}

func TestMarketplaceTypedListingFiltersStayOperationSpecific(t *testing.T) {
	keyword := "  lamp  "
	status := "published"
	minimum, maximum := int64(100), int64(200)
	page, pageSize := int32(2), int32(50)

	publicParams := generated.ListMarketplaceListingsParams{
		Keyword: &keyword, MinPriceCents: &minimum, MaxPriceCents: &maximum,
		Page: &page, PageSize: &pageSize,
	}
	publicSearch := marketplaceSearchFromPublicParams(publicParams)
	hasPublicFilters := publicSearch.Keyword == "lamp" && publicSearch.Status == "" &&
		publicSearch.MinPriceCents != nil && publicSearch.MaxPriceCents != nil
	hasPublicPaging := publicSearch.Page == 2 && publicSearch.PageSize == 50
	if !hasPublicFilters || !hasPublicPaging {
		t.Fatalf("public search = %#v", publicSearch)
	}

	mineParams := generated.ListMyMarketplaceListingsParams{Status: &status, Page: &page, PageSize: &pageSize}
	mineSearch := marketplaceSearchFromMineParams(mineParams)
	hasMineFilters := mineSearch.Status == status && mineSearch.Keyword == "" &&
		mineSearch.MinPriceCents == nil && mineSearch.MaxPriceCents == nil
	if !hasMineFilters {
		t.Fatalf("mine search = %#v", mineSearch)
	}

	adminParams := generated.ListAdminMarketplaceListingsParams{
		Status: &status, Keyword: &keyword, MinPriceCents: &minimum, MaxPriceCents: &maximum,
		Page: &page, PageSize: &pageSize,
	}
	adminSearch := marketplaceSearchFromAdminParams(adminParams)
	hasAdminFilters := adminSearch.Status == status && adminSearch.Keyword == "lamp" &&
		adminSearch.MinPriceCents != nil && *adminSearch.MinPriceCents == minimum &&
		adminSearch.MaxPriceCents != nil && *adminSearch.MaxPriceCents == maximum
	hasAdminPaging := adminSearch.Page == 2 && adminSearch.PageSize == 50
	if !hasAdminFilters || !hasAdminPaging {
		t.Fatalf("admin search = %#v", adminSearch)
	}
}
