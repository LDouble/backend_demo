package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCanonicalRequestHashPreservesLargeJSONNumbers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hashes := make([]string, 0, 2)
	for _, body := range []string{
		`{"id":9007199254740992}`,
		`{"id":9007199254740993}`,
	} {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(body))
		hash, err := canonicalRequestHash(context)
		if err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
	}
	if hashes[0] == hashes[1] {
		t.Fatalf("distinct large JSON numbers produced the same hash: %s", hashes[0])
	}
}

func TestCanonicalRequestHashRejectsMultipleJSONValues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(`{"id":1} {"id":2}`))
	if _, err := canonicalRequestHash(context); err == nil {
		t.Fatal("multiple JSON values were accepted")
	}
}
