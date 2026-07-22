package httpapi

import (
	"testing"

	"github.com/weouc-plus/campus-platform/internal/api/generated"
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
