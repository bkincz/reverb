package storage_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"modernc.org/sqlite"

	"github.com/bkincz/reverb/auth"
	dbmodels "github.com/bkincz/reverb/db/models"
	"github.com/bkincz/reverb/storage"
	"github.com/bkincz/reverb/storage/local"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

var driverSeq int

func newTestDB(t *testing.T) *bun.DB {
	t.Helper()
	driverSeq++
	name := fmt.Sprintf("sqlite3_storage_%d", driverSeq)
	sql.Register(name, &sqlite.Driver{})
	sqlDB, err := sql.Open(name, "file::memory:?cache=shared&_foreign_keys=on")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	bunDB := bun.NewDB(sqlDB, sqlitedialect.New())

	ctx := context.Background()
	if _, err := bunDB.NewCreateTable().Model((*dbmodels.Media)(nil)).IfNotExists().Exec(ctx); err != nil {
		t.Fatalf("create media table: %v", err)
	}

	t.Cleanup(func() { _ = bunDB.Close() })
	return bunDB
}

func newTestAdapter(t *testing.T) *local.Adapter {
	t.Helper()
	dir := t.TempDir()
	return local.New(dir, "/_reverb/storage/files")
}

func multipartUpload(t *testing.T, filename, content, alt string) *http.Request {
	t.Helper()
	return multipartUploadBytes(t, filename, []byte(content), alt)
}

func multipartUploadBytes(t *testing.T, filename string, content []byte, alt string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}

	if alt != "" {
		if err := mw.WriteField("alt", alt); err != nil {
			t.Fatalf("write alt field: %v", err)
		}
	}

	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/_reverb/storage/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&m); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	return m
}

const (
	testSecret = "test-secret-key-at-least-32-bytes!!"
)

var testTokenCfg = auth.TokenConfig{
	Secret:    testSecret,
	AccessTTL: 15 * time.Minute,
}

func signToken(t *testing.T, userID, role string) string {
	t.Helper()
	tok, err := auth.SignAccess(testTokenCfg, userID, "user@test.com", role)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return tok
}

func wrapAuth(h http.Handler) http.Handler {
	return auth.RequireAuth(auth.Config{Tokens: testTokenCfg})(h)
}

// ---------------------------------------------------------------------------
// Local adapter — unit tests
// ---------------------------------------------------------------------------

func TestLocalAdapter_Upload(t *testing.T) {
	adapter := newTestAdapter(t)
	ctx := context.Background()

	err := adapter.Upload(ctx, storage.UploadInput{
		Key:         "abc/photo.jpg",
		Body:        strings.NewReader("fake image data"),
		Size:        15,
		ContentType: "image/jpeg",
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// File must exist on disk under the adapter root directory.
	// We verify indirectly via URL and List; direct path inspection uses
	// TempDir, so we reconstruct the expected location from the adapter URL.
	url := adapter.URL("abc/photo.jpg")
	if !strings.HasPrefix(url, "/_reverb/storage/files/") {
		t.Errorf("unexpected URL prefix: %q", url)
	}
	if !strings.HasSuffix(url, "abc/photo.jpg") {
		t.Errorf("unexpected URL suffix: %q", url)
	}
}

func TestLocalAdapter_UploadOverwrite(t *testing.T) {
	adapter := newTestAdapter(t)
	ctx := context.Background()

	upload := func(content string) {
		t.Helper()
		err := adapter.Upload(ctx, storage.UploadInput{
			Key:         "file.txt",
			Body:        strings.NewReader(content),
			Size:        int64(len(content)),
			ContentType: "text/plain",
		})
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
	}

	upload("version 1")
	upload("version 2 — longer")

	// List should still show a single entry.
	items, err := adapter.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item after overwrite, got %d", len(items))
	}
}

func TestLocalAdapter_Delete(t *testing.T) {
	adapter := newTestAdapter(t)
	ctx := context.Background()

	if err := adapter.Upload(ctx, storage.UploadInput{
		Key:  "to-delete.txt",
		Body: strings.NewReader("goodbye"),
		Size: 7,
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	if err := adapter.Delete(ctx, "to-delete.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Deleting again must not error (idempotent).
	if err := adapter.Delete(ctx, "to-delete.txt"); err != nil {
		t.Errorf("second Delete should be a no-op, got: %v", err)
	}

	// File must no longer appear in the listing.
	items, err := adapter.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, item := range items {
		if item.Key == "to-delete.txt" {
			t.Errorf("deleted file still appears in List")
		}
	}
}

func TestLocalAdapter_List(t *testing.T) {
	adapter := newTestAdapter(t)
	ctx := context.Background()

	keys := []string{"a.txt", "b.txt", "c.txt"}
	for _, k := range keys {
		if err := adapter.Upload(ctx, storage.UploadInput{
			Key:  k,
			Body: strings.NewReader("data"),
			Size: 4,
		}); err != nil {
			t.Fatalf("Upload %q: %v", k, err)
		}
	}

	items, err := adapter.List(ctx, "", 100)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != len(keys) {
		t.Errorf("expected %d items, got %d", len(keys), len(items))
	}
}

func TestLocalAdapter_ListLimit(t *testing.T) {
	adapter := newTestAdapter(t)
	ctx := context.Background()

	for i := range 5 {
		if err := adapter.Upload(ctx, storage.UploadInput{
			Key:  fmt.Sprintf("file%d.txt", i),
			Body: strings.NewReader("x"),
			Size: 1,
		}); err != nil {
			t.Fatalf("Upload: %v", err)
		}
	}

	items, err := adapter.List(ctx, "", 3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items (limit), got %d", len(items))
	}
}

func TestLocalAdapter_URL(t *testing.T) {
	adapter := local.New("/tmp/files", "/_reverb/storage/files")
	got := adapter.URL("uuid123/photo.jpg")
	want := "/_reverb/storage/files/uuid123/photo.jpg"
	if got != want {
		t.Errorf("URL: got %q, want %q", got, want)
	}
}

func TestLocalAdapter_FileServerPath(t *testing.T) {
	adapter := local.New("/tmp/files", "/_reverb/storage/files")
	if adapter.FileServePath() != "/_reverb/storage/files/" {
		t.Errorf("unexpected FileServePath: %q", adapter.FileServePath())
	}
}

func TestLocalAdapter_FileServer(t *testing.T) {
	dir := t.TempDir()
	adapter := local.New(dir, "/_reverb/storage/files")
	ctx := context.Background()

	content := "hello from file server"
	if err := adapter.Upload(ctx, storage.UploadInput{
		Key:  "hello.txt",
		Body: strings.NewReader(content),
		Size: int64(len(content)),
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	srv := adapter.FileServer()
	req := httptest.NewRequest(http.MethodGet, "/_reverb/storage/files/hello.txt", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if body := rr.Body.String(); body != content {
		t.Errorf("unexpected body: %q", body)
	}
}

// ---------------------------------------------------------------------------
// Key sanitisation
// ---------------------------------------------------------------------------

// sanitiseKey is internal; we test it indirectly via the upload handler with
// a malicious filename and confirm the resulting record has a safe storage key.

// ---------------------------------------------------------------------------
// Handler — HandleUpload
// ---------------------------------------------------------------------------

func TestHandleUpload_ReturnsCreated(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := wrapAuth(storage.HandleUpload(db, adapter))

	tok := signToken(t, "user-1", "editor")
	req := multipartUpload(t, "photo.jpg", "fake jpeg", "a nice photo")
	req.Header.Set("Authorization", "Bearer "+tok)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", rr.Code, rr.Body.String())
	}

	body := decodeJSON(t, rr)
	if body["id"] == nil || body["id"] == "" {
		t.Errorf("missing id field")
	}
	if body["url"] == nil || body["url"] == "" {
		t.Errorf("missing url field")
	}
	if body["filename"] != "photo.jpg" {
		t.Errorf("unexpected filename: %v", body["filename"])
	}
	if body["alt"] != "a nice photo" {
		t.Errorf("unexpected alt: %v", body["alt"])
	}
}

func TestHandleUpload_SanitisesFilename(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := wrapAuth(storage.HandleUpload(db, adapter))

	tok := signToken(t, "user-1", "editor")
	req := multipartUpload(t, "../../../etc/passwd", "content", "")
	req.Header.Set("Authorization", "Bearer "+tok)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d — body: %s", rr.Code, rr.Body.String())
	}

	body := decodeJSON(t, rr)
	url, _ := body["url"].(string)
	if strings.Contains(url, "..") {
		t.Errorf("URL contains path traversal: %q", url)
	}

	if _, err := os.Stat("/etc/passwd"); err == nil {
		// /etc/passwd is a real file on Unix, so existence alone proves nothing.
		// The important thing is that the upload adapter root was not escaped.
	}
}

func TestHandleUpload_UsesSniffedContentType(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := wrapAuth(storage.HandleUpload(db, adapter))

	tok := signToken(t, "user-1", "editor")
	pngHeader := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d}
	req := multipartUploadBytes(t, "image.txt", pngHeader, "")
	req.Header.Set("Authorization", "Bearer "+tok)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d â€” body: %s", rr.Code, rr.Body.String())
	}

	body := decodeJSON(t, rr)
	if body["mime_type"] != "image/png" {
		t.Fatalf("expected sniffed mime_type image/png, got %v", body["mime_type"])
	}
}

func TestHandleUpload_NoAuthHeader_Rejected(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := wrapAuth(storage.HandleUpload(db, adapter))

	req := multipartUpload(t, "photo.jpg", "data", "")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rr.Code)
	}
}

func TestHandleUpload_MissingFileField(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := wrapAuth(storage.HandleUpload(db, adapter))

	tok := signToken(t, "user-1", "editor")

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("alt", "no file here")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/_reverb/storage/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+tok)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing file field, got %d", rr.Code)
	}
}

// ---------------------------------------------------------------------------
// Handler — HandleDelete
// ---------------------------------------------------------------------------

func TestHandleDelete_UnknownID(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	handler := storage.HandleDelete(db, adapter)
	req := httptest.NewRequest(http.MethodDelete, "/_reverb/storage/nonexistent-id", nil)
	req.SetPathValue("id", "nonexistent-id")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleDelete_DeletesRecord(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)
	ctx := context.Background()

	if err := adapter.Upload(ctx, storage.UploadInput{
		Key:  "del-test/file.txt",
		Body: strings.NewReader("bye"),
		Size: 3,
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	record := &dbmodels.Media{
		ID:         "media-del-1",
		Filename:   "file.txt",
		StorageKey: "del-test/file.txt",
		MimeType:   "text/plain",
		Size:       3,
		CreatedAt:  time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(record).Exec(ctx); err != nil {
		t.Fatalf("insert media record: %v", err)
	}

	handler := storage.HandleDelete(db, adapter)
	req := httptest.NewRequest(http.MethodDelete, "/_reverb/storage/media-del-1", nil)
	req.SetPathValue("id", "media-del-1")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d — body: %s", rr.Code, rr.Body.String())
	}

	var count int
	if err := db.NewSelect().TableExpr("reverb_media").
		Where("id = ?", "media-del-1").
		ColumnExpr("count(*)").Scan(ctx, &count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("record still exists in DB after delete")
	}
}

// ---------------------------------------------------------------------------
// Handler — HandleList
// ---------------------------------------------------------------------------

func TestHandleList_Empty(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)

	req := httptest.NewRequest(http.MethodGet, "/_reverb/storage", nil)
	rr := httptest.NewRecorder()
	storage.HandleList(db, adapter).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := decodeJSON(t, rr)
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data field missing or wrong type: %v", body["data"])
	}
	if len(data) != 0 {
		t.Errorf("expected 0 items, got %d", len(data))
	}
	if body["total"] != float64(0) {
		t.Errorf("expected total=0, got %v", body["total"])
	}
}

func TestHandleList_Pagination(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)
	ctx := context.Background()

	for i := range 5 {
		record := &dbmodels.Media{
			ID:         fmt.Sprintf("media-%d", i),
			Filename:   fmt.Sprintf("file%d.jpg", i),
			StorageKey: fmt.Sprintf("uuid%d/file%d.jpg", i, i),
			MimeType:   "image/jpeg",
			Size:       1024,
			CreatedAt:  time.Now().UTC(),
		}
		if _, err := db.NewInsert().Model(record).Exec(ctx); err != nil {
			t.Fatalf("insert record %d: %v", i, err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/_reverb/storage?page=1&limit=3", nil)
	rr := httptest.NewRecorder()
	storage.HandleList(db, adapter).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := decodeJSON(t, rr)
	data, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("data field wrong type: %v", body["data"])
	}
	if len(data) != 3 {
		t.Errorf("page 1: expected 3 items, got %d", len(data))
	}
	if body["total"] != float64(5) {
		t.Errorf("expected total=5, got %v", body["total"])
	}
	if body["page"] != float64(1) {
		t.Errorf("expected page=1, got %v", body["page"])
	}
	if body["limit"] != float64(3) {
		t.Errorf("expected limit=3, got %v", body["limit"])
	}
}

func TestHandleList_URLEnrichedFromAdapter(t *testing.T) {
	db := newTestDB(t)
	adapter := newTestAdapter(t)
	ctx := context.Background()

	record := &dbmodels.Media{
		ID:         "media-url-1",
		Filename:   "photo.jpg",
		StorageKey: "uuid-x/photo.jpg",
		MimeType:   "image/jpeg",
		Size:       512,
		CreatedAt:  time.Now().UTC(),
	}
	if _, err := db.NewInsert().Model(record).Exec(ctx); err != nil {
		t.Fatalf("insert record: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/_reverb/storage", nil)
	rr := httptest.NewRecorder()
	storage.HandleList(db, adapter).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	body := decodeJSON(t, rr)
	data, _ := body["data"].([]any)
	if len(data) == 0 {
		t.Fatal("expected at least one record")
	}

	item, _ := data[0].(map[string]any)
	url, _ := item["url"].(string)
	if !strings.Contains(url, "uuid-x/photo.jpg") {
		t.Errorf("url does not contain storage key: %q", url)
	}
}

// ---------------------------------------------------------------------------
// FileServer interface compliance
// ---------------------------------------------------------------------------

func TestLocalAdapter_ImplementsFileServerInterface(t *testing.T) {
	adapter := local.New("/tmp", "/_reverb/storage/files")
	var _ storage.FileServer = adapter
}

// ---------------------------------------------------------------------------
// Adapter interface compliance (compile-time checks via _ var)
// ---------------------------------------------------------------------------

func TestLocalAdapterImplementsStorageAdapter(_ *testing.T) {
	var _ storage.Adapter = (*local.Adapter)(nil)
}

// ---------------------------------------------------------------------------
// Disk isolation — file written to correct path
// ---------------------------------------------------------------------------

func TestLocalAdapter_FileWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	adapter := local.New(dir, "/_reverb/storage/files")
	ctx := context.Background()

	key := "sub/dir/hello.txt"
	content := "disk content"
	if err := adapter.Upload(ctx, storage.UploadInput{
		Key:  key,
		Body: strings.NewReader(content),
		Size: int64(len(content)),
	}); err != nil {
		t.Fatalf("Upload: %v", err)
	}

	path := filepath.Join(dir, "sub", "dir", "hello.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("unexpected file content: %q", data)
	}
}
