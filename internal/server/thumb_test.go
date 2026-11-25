package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"lfh/internal/auth"
	"lfh/internal/cluster"
	"lfh/internal/storage"
)

func uploadTestBytes(t *testing.T, client *http.Client, baseURL, filename string, data []byte, allowed []string) storage.FileRecord {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("files", filename)
	if err != nil {
		t.Fatalf("CreateFormFile %s: %v", filename, err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatalf("Write %s: %v", filename, err)
	}
	if len(allowed) > 0 {
		for _, u := range allowed {
			_ = mw.WriteField("allowed_users", u)
		}
	}
	_ = mw.Close()

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/upload", &buf)
	if err != nil {
		t.Fatalf("NewRequest upload: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload %s status=%d", filename, resp.StatusCode)
	}
	var out struct {
		Uploaded []storage.FileRecord `json:"uploaded"`
		File     storage.FileRecord   `json:"file"`
		Files    []storage.FileRecord `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode upload resp: %v", err)
	}
	if len(out.Uploaded) > 0 {
		return out.Uploaded[0]
	}
	if out.File.ID != "" {
		return out.File
	}
	if len(out.Files) > 0 {
		return out.Files[0]
	}
	t.Fatalf("missing uploaded file record in response")
	return storage.FileRecord{}
}

func makeTestPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 59, G: 130, B: 246, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func createAndLoginMember(t *testing.T, adminClient *http.Client, baseURL, username, password string) *http.Client {
	t.Helper()
	invResp, err := adminClient.Post(baseURL+"/api/admin/invites", "application/json", strings.NewReader(`{"note":"thumb-test","max_uses":1,"expire_hours":24}`))
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	defer invResp.Body.Close()
	if invResp.StatusCode != http.StatusCreated {
		t.Fatalf("invite status=%d", invResp.StatusCode)
	}
	var created struct {
		Invite auth.InviteCode `json:"invite"`
	}
	if err := json.NewDecoder(invResp.Body).Decode(&created); err != nil {
		t.Fatalf("decode invite: %v", err)
	}

	c := newCookieClient(t)
	regBody, _ := json.Marshal(map[string]string{
		"username":    username,
		"password":    password,
		"invite_code": created.Invite.Code,
	})
	regResp, err := c.Post(baseURL+"/api/register", "application/json", bytes.NewReader(regBody))
	if err != nil {
		t.Fatalf("register %s: %v", username, err)
	}
	regResp.Body.Close()
	if regResp.StatusCode != http.StatusCreated && regResp.StatusCode != http.StatusOK {
		t.Fatalf("register %s status=%d", username, regResp.StatusCode)
	}

	loginBody, _ := json.Marshal(map[string]string{
		"username": username,
		"password": password,
	})
	loginResp, err := c.Post(baseURL+"/api/login", "application/json", bytes.NewReader(loginBody))
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login %s status=%d", username, loginResp.StatusCode)
	}
	return c
}

func TestThumbnailEndpointETagVisibilityAndUnsupported(t *testing.T) {
	srv, ts := newTestServer(t)
	defer srv.CloseEventStreams()

	adminClient := loginTestAdmin(t, srv, ts.URL)
	adminUser, _, _ := srv.auth.InitialAdminCredentials()
	bobClient := createAndLoginMember(t, adminClient, ts.URL, "bob", "Pass123456!")

	pngBytes := makeTestPNGBytes(t, 640, 320)
	imgRec := uploadTestBytes(t, adminClient, ts.URL, "sample.png", pngBytes, []string{adminUser})
	// Explicitly lock visibility via /api/files/{id}/access to guarantee isolation regardless of upload multipart parsing.
	lockBody, _ := json.Marshal(map[string]any{"allowed_users": []string{adminUser}})
	lockResp, err := adminClient.Post(ts.URL+"/api/files/"+imgRec.ID+"/access", "application/json", bytes.NewReader(lockBody))
	if err != nil {
		t.Fatalf("lock access: %v", err)
	}
	lockResp.Body.Close()
	if lockResp.StatusCode != http.StatusOK {
		t.Fatalf("lock access status=%d", lockResp.StatusCode)
	}

	txtRec := uploadTestBytes(t, adminClient, ts.URL, "notes.txt", bytes.Repeat([]byte("line-abcdef\n"), 500), []string{adminUser})

	// 1. Admin fetches 160px thumbnail -> 200, 160x80 image/jpeg, ETag present.
	resp, err := adminClient.Get(ts.URL + "/api/files/" + imgRec.ID + "/thumb?w=160")
	if err != nil {
		t.Fatalf("GET thumb: %v", err)
	}
	var thumbBody bytes.Buffer
	_, _ = thumbBody.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("thumb status = %d body=%s", resp.StatusCode, thumbBody.String())
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("expected non-empty ETag on thumbnail response")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(thumbBody.Bytes()))
	if err != nil {
		t.Fatalf("decode thumb output: %v", err)
	}
	if cfg.Width != 160 || cfg.Height != 80 {
		t.Fatalf("expected 160x80, got %dx%d", cfg.Width, cfg.Height)
	}

	// 2. Matching If-None-Match returns 304 Not Modified.
	req304, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/files/"+imgRec.ID+"/thumb?w=160", nil)
	req304.Header.Set("If-None-Match", etag)
	resp304, err := adminClient.Do(req304)
	if err != nil {
		t.Fatalf("GET thumb 304: %v", err)
	}
	resp304.Body.Close()
	if resp304.StatusCode != http.StatusNotModified {
		t.Fatalf("expected 304 Not Modified, got %d", resp304.StatusCode)
	}

	// 3. Unauthorized user bob cannot access admin-restricted thumbnail.
	respUnauth, err := bobClient.Get(ts.URL + "/api/files/" + imgRec.ID + "/thumb?w=160")
	if err != nil {
		t.Fatalf("GET thumb unauth: %v", err)
	}
	respUnauth.Body.Close()
	if respUnauth.StatusCode != http.StatusForbidden && respUnauth.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 403 or 404 for unauthorized user, got %d", respUnauth.StatusCode)
	}

	// 4. Unsupported ext (.txt) returns 415 Unsupported Media Type.
	resp415, err := adminClient.Get(ts.URL + "/api/files/" + txtRec.ID + "/thumb")
	if err != nil {
		t.Fatalf("GET thumb txt: %v", err)
	}
	resp415.Body.Close()
	if resp415.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("expected 415 for txt thumbnail, got %d", resp415.StatusCode)
	}

	// 5. Snippet preview caps output to <= 2048 bytes and marks snippet=true.
	respSnip, err := adminClient.Get(ts.URL + "/api/files/" + txtRec.ID + "/preview?snippet=1")
	if err != nil {
		t.Fatalf("GET preview snippet: %v", err)
	}
	defer respSnip.Body.Close()
	if respSnip.StatusCode != http.StatusOK {
		t.Fatalf("snippet status=%d", respSnip.StatusCode)
	}
	var snipOut struct {
		Snippet   bool   `json:"snippet"`
		Truncated bool   `json:"truncated"`
		Content   string `json:"content"`
	}
	if err := json.NewDecoder(respSnip.Body).Decode(&snipOut); err != nil {
		t.Fatalf("decode snippet: %v", err)
	}
	if !snipOut.Snippet || !snipOut.Truncated || len(snipOut.Content) > 2048 || len(snipOut.Content) == 0 {
		t.Fatalf("unexpected snippet output: snippet=%v truncated=%v len=%d", snipOut.Snippet, snipOut.Truncated, len(snipOut.Content))
	}

	// 6. .mjs MIME registration contains javascript.
	mjsType := strings.ToLower(mime.TypeByExtension(".mjs"))
	if !strings.Contains(mjsType, "javascript") {
		t.Fatalf("expected .mjs mime type to contain javascript, got %q", mjsType)
	}

	_ = cluster.RoleEntry
}
