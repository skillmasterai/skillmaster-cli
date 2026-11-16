package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// **A retired generation is not a version or skill problem.** The answer is a reinstall, so the
// sentence has to name the generation this binary speaks and the formal install document — a bare
// 410 would leave the person with a status code and no next step (v79, RELEASE.md §9).
func TestRetiredGenerationSaysWhatToReinstall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"error":{"code":"api_generation_retired","message":"server text"}}`))
	}))
	defer server.Close()

	_, err := Client{BaseURL: server.URL}.Detail(context.Background(), "dev/pdf-tools", "1.2.3")
	if err == nil {
		t.Fatal("a retired generation reported success")
	}
	var failure *ServerError
	if !errors.As(err, &failure) || failure.Code != "api_generation_retired" {
		t.Fatalf("err = %v (%T)", err, err)
	}
	for _, want := range []string{"API " + Generation, "https://dl.skillmasterai.com/skillmaster.md", "重装"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the sentence is missing %q: %v", want, err)
		}
	}
}
