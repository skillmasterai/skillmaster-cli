package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An upload refusal keeps the server's envelope, because `details[]` names the bad field and a
// message alone would lose it.
func TestUploadRefusalsKeepTheirDetails(t *testing.T) {
	upload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_upload","message":"bad frontmatter",
			"details":[{"field":"description","issue":"missing"}]}}`))
	}))
	defer upload.Close()

	_, err := Client{BaseURL: upload.URL, Token: "t"}.Submit(context.Background(), []byte("zip"),
		"a name", "", "")
	if err == nil {
		t.Fatal("an invalid upload reported success")
	}
	// **v33: the details travel as details.** Before, they were reachable only by reading the raw
	// envelope out of `Error()`, which is how `submit` came to print a JSON blob; a `ServerError`
	// now keeps them in order, which is what the per-file refusal renders from.
	var uploadFailure *ServerError
	if !errors.As(err, &uploadFailure) || uploadFailure.Code != "invalid_upload" {
		t.Fatalf("err = %v, want a ServerError carrying the upload's code", err)
	}
	if uploadFailure.Details["description"] != "missing" || uploadFailure.Body == "" {
		t.Errorf("err = %#v, want the details that name the field", uploadFailure)
	}

	// **413 is the same envelope** (v39). Size refusals moved from 400 to 413, and the branch above
	// was gated on 400 — so `submit` printed the raw JSON instead of the import-ceiling sentence.
	tooLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_, _ = w.Write([]byte(`{"error":{"code":"invalid_upload","message":"the package expands to 300 MiB",
			"details":[{"field":"file","issue":"package_too_large","actual_bytes":314572800,"limit_bytes":134217728}]}}`))
	}))
	defer tooLarge.Close()

	_, err = Client{BaseURL: tooLarge.URL, Token: "t"}.Submit(context.Background(), []byte("zip"),
		"a name", "", "")
	var overCeiling *ServerError
	if !errors.As(err, &overCeiling) || overCeiling.Code != "invalid_upload" {
		t.Fatalf("err = %v, want a ServerError carrying the 413 envelope", err)
	}
	if len(overCeiling.DetailList) != 1 || overCeiling.DetailList[0].LimitBytes != 134217728 ||
		overCeiling.DetailList[0].ActualBytes != 314572800 {
		t.Errorf("details = %#v, want the snake_case byte counts", overCeiling.DetailList)
	}

	// **A 409 keeps them too** (v76). `skill submit` creates a skill when the name is new, so it is
	// one of the paths the namespace ceiling answers; the branch used to build a `ServerError` by
	// hand, and the quota sentence — which needs both counts — lost them and named the wrong pool.
	overQuota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"skill_quota_exceeded","message":"full",
			"details":[{"field":"quota","issue":"namespace","actual_count":200,"limit_count":200}]}}`))
	}))
	defer overQuota.Close()

	_, err = Client{BaseURL: overQuota.URL, Token: "t"}.Submit(context.Background(), []byte("zip"),
		"a name", "", "")
	var full *ServerError
	if !errors.As(err, &full) || full.Code != "skill_quota_exceeded" {
		t.Fatalf("err = %v, want a ServerError carrying the quota code", err)
	}
	if len(full.DetailList) != 1 || full.DetailList[0].ActualCount != 200 ||
		full.DetailList[0].LimitCount != 200 || full.DetailList[0].Issue != "namespace" {
		t.Fatalf("details = %#v, want the two counts and the pool", full.DetailList)
	}
	if got := full.Error(); !strings.Contains(got, "200") || !strings.Contains(got, "这个命名空间") {
		t.Errorf("sentence = %q, want the namespace sentence with the numbers", got)
	}
}
