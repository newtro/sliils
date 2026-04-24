//go:build integration

package server_test

// M5 file upload / attach / download tests.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uploadBody is the multipart request body for POST /api/v1/files.
func uploadBody(t *testing.T, workspaceID int64, filename string, contents []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("workspace_id", strconv.FormatInt(workspaceID, 10)))
	part, err := w.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write(contents)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

// uploadFile posts a multipart upload and returns the parsed response.
func uploadFile(t *testing.T, h *testHarness, token string, workspaceID int64, filename string, contents []byte) map[string]any {
	t.Helper()
	body, ct := uploadBody(t, workspaceID, filename, contents)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	require.Contains(t, []int{http.StatusCreated, http.StatusOK}, rec.Code,
		"upload status=%d body=%s", rec.Code, rec.Body.String())
	var out map[string]any
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&out))
	return out
}

// smallPNG returns a valid 20x20 red PNG.
func smallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	red := color.RGBA{R: 200, G: 40, B: 40, A: 255}
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, red)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func TestFileUploadHappyPath(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "uploader@m5.test", "correct-horse-battery-staple")
	drainEmails(h)
	ws := createWorkspace(t, h, resp.AccessToken, "MsgCo", "msg-co-m5")

	png := smallPNG(t)
	dto := uploadFile(t, h, resp.AccessToken, ws.ID, "red.png", png)
	assert.EqualValues(t, "red.png", dto["filename"])
	assert.EqualValues(t, "image/png", dto["mime"])
	assert.EqualValues(t, 20, dto["width"])
	assert.EqualValues(t, 20, dto["height"])

	// URL is ours; fetch it back.
	url := dto["url"].(string)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	body, _ := io.ReadAll(rec.Body)
	assert.NotZero(t, len(body))
	assert.Contains(t, rec.Header().Get("Content-Type"), "image/png")
}

func TestFileUploadIsDedupedBySHA256(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "dedupe@m5.test", "correct-horse-battery-staple")
	drainEmails(h)
	ws := createWorkspace(t, h, resp.AccessToken, "DCo", "dedupe-co")

	bin := []byte("file content goes here")
	dto1 := uploadFile(t, h, resp.AccessToken, ws.ID, "a.txt", bin)
	dto2 := uploadFile(t, h, resp.AccessToken, ws.ID, "a-duplicate.txt", bin)
	assert.Equal(t, dto1["id"], dto2["id"],
		"same bytes in same workspace must resolve to the same file id")
}

func TestFileCrossWorkspaceRLS(t *testing.T) {
	h := newHarness(t)
	respA, _ := signup(t, h, "alice@files.test", "correct-horse-battery-staple")
	drainEmails(h)
	respB, _ := signup(t, h, "bob@files.test", "correct-horse-battery-staple")
	drainEmails(h)

	wsA := createWorkspace(t, h, respA.AccessToken, "Alice Co", "alice-files")
	createWorkspace(t, h, respB.AccessToken, "Bob Co", "bob-files")

	// Alice uploads to her workspace.
	dto := uploadFile(t, h, respA.AccessToken, wsA.ID, "secret.txt", []byte("top secret"))
	fileID := int64(dto["id"].(float64))

	// Bob should get 404 on download — the file is hidden by RLS.
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/files/%d/raw", fileID), nil)
	req.Header.Set("Authorization", "Bearer "+respB.AccessToken)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"user B must not reach user A's file (expected 404)")

	// Bob uploading to Alice's workspace must also fail.
	body, ct := uploadBody(t, wsA.ID, "intrusion.txt", []byte("bob here"))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/files", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+respB.AccessToken)
	rec = httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestMessageWithAttachments(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "attach@m5.test", "correct-horse-battery-staple")
	drainEmails(h)
	ws := createWorkspace(t, h, resp.AccessToken, "Attach Co", "attach-co")
	chID := firstChannelID(t, h, resp.AccessToken, "attach-co")

	dto := uploadFile(t, h, resp.AccessToken, ws.ID, "doc.txt", []byte("hello attachment"))
	fileID := int64(dto["id"].(float64))

	body := fmt.Sprintf(`{"body_md":"see attached","attachment_ids":[%d]}`, fileID)
	rec := h.postAuth(fmt.Sprintf("/api/v1/channels/%d/messages", chID), body, resp.AccessToken)
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	var m struct {
		ID          int64 `json:"id"`
		Attachments []struct {
			ID       int64  `json:"id"`
			Filename string `json:"filename"`
		} `json:"attachments"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&m))
	require.Len(t, m.Attachments, 1)
	assert.Equal(t, fileID, m.Attachments[0].ID)

	// List also hydrates attachments.
	rec = h.get(fmt.Sprintf("/api/v1/channels/%d/messages", chID), resp.AccessToken)
	var list struct {
		Messages []struct {
			ID          int64 `json:"id"`
			Attachments []struct {
				ID int64 `json:"id"`
			} `json:"attachments"`
		} `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&list))
	require.Len(t, list.Messages, 1)
	require.Len(t, list.Messages[0].Attachments, 1)
	assert.Equal(t, fileID, list.Messages[0].Attachments[0].ID)
}

func TestUploadRejectsEmpty(t *testing.T) {
	h := newHarness(t)
	resp, _ := signup(t, h, "empty@m5.test", "correct-horse-battery-staple")
	drainEmails(h)
	ws := createWorkspace(t, h, resp.AccessToken, "E", "empty-co")

	body, ct := uploadBody(t, ws.ID, "empty.txt", []byte{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/files", body)
	req.Header.Set("Content-Type", ct)
	req.Header.Set("Authorization", "Bearer "+resp.AccessToken)
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
