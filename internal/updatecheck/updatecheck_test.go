package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckFindsNewerReleaseByTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "costrict-router/v0.3" {
			t.Fatalf("User-Agent = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"v0.3.1","tag_name":"v0.3.1"}`)
	}))
	defer server.Close()

	latest, available, err := check(context.Background(), server.Client(), server.URL, "v0.3")
	if err != nil {
		t.Fatal(err)
	}
	if latest != "v0.3.1" || !available {
		t.Fatalf("latest = %q, available = %t", latest, available)
	}
}

func TestCheckFallsBackToTagAndComparesNumerically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"name":"","tag_name":"v0.10"}`)
	}))
	defer server.Close()

	latest, available, err := check(context.Background(), server.Client(), server.URL, "v0.9.8")
	if err != nil {
		t.Fatal(err)
	}
	if latest != "v0.10" || !available {
		t.Fatalf("latest = %q, available = %t", latest, available)
	}
}

func TestCheckDoesNotReportSameOrOlderRelease(t *testing.T) {
	for _, latest := range []string{"v0.3", "v0.2.9"} {
		t.Run(latest, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"name":%q}`, latest)
			}))
			defer server.Close()

			_, available, err := check(context.Background(), server.Client(), server.URL, "v0.3")
			if err != nil {
				t.Fatal(err)
			}
			if available {
				t.Fatal("unexpected update notification")
			}
		})
	}
}

func TestParseSemanticVersionRejectsDevelopmentBuilds(t *testing.T) {
	for _, raw := range []string{
		"dev",
		"v0.3-4-gabcdef",
		"v0.3-alpha.3",
		"v0.3.1-beta.2",
		"",
		"v1",
	} {
		if _, ok := parseSemanticVersion(raw); ok {
			t.Fatalf("parseSemanticVersion(%q) unexpectedly succeeded", raw)
		}
	}
}
