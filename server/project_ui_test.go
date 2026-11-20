package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func uiFixture(t *testing.T) (*projectPlanner, project) {
	t.Helper()
	p, item := plannerFixture(t, "normal")
	doc := samplePlan()
	doc.Nodes[0].Kind = "page"
	doc.Nodes[0].Title = "测试页面"
	raw, _ := json.Marshal(doc)
	_, err := p.db.Exec(`INSERT INTO plan_versions VALUES(?,?,?,?)`, item.ID, 1, string(raw), time.Now().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	return p, item
}
func tinyPNG() []byte {
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 5))
	img.Set(1, 1, color.White)
	_ = png.Encode(&b, img)
	return b.Bytes()
}
func putUIFixture(t *testing.T, item project, file string, data []byte) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(filepath.Join(item.Root, file)), 0700)
	if err := os.WriteFile(filepath.Join(item.Root, file), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestUICatalogFindsPreviewsAndDocumentLinksWithoutInventingScreens(t *testing.T) {
	p, item := uiFixture(t)
	putUIFixture(t, item, "previews/native.png", tinyPNG())
	putUIFixture(t, item, "shots/current.png", tinyPNG())
	putUIFixture(t, item, "README.md", []byte("# App\n真实界面 [截图](shots/current.png)\n"))
	putUIFixture(t, item, "previews/Old.app/Contents/secret.png", tinyPNG())
	_ = os.Symlink("../shots", filepath.Join(item.Root, "previews", "linked"))
	assets, _ := discoverUIAssets(item)
	if len(assets) != 2 {
		t.Fatalf("unexpected media %+v", assets)
	}
	var proof bool
	for _, asset := range assets {
		if asset.File == "shots/current.png" {
			proof = len(asset.References) == 1 && asset.References[0].Line == 2
		}
	}
	if !proof {
		t.Fatal("missing README association evidence")
	}
	w := plannerRequest(p, "GET", "/api/projects/"+item.ID+"/app-ui", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Groups []struct {
			Screens []struct {
				NodeID   string `json:"nodeId"`
				Captures []planCapture
			}
		}
		UI projectUIView
	}
	_ = json.Unmarshal(w.Body.Bytes(), &response)
	if response.UI.Total != 1 || response.UI.WithPreview != 0 || len(response.Groups) != 1 || response.Groups[0].Screens[0].NodeID != "main" {
		t.Fatalf("source-only page absent or images became fake pages: %s", w.Body.String())
	}
}
func TestUIPreviewBindingUnifiesBothViewsAndInvalidatesChangedAssets(t *testing.T) {
	p, item := uiFixture(t)
	file := "previews/native.png"
	putUIFixture(t, item, file, tinyPNG())
	base := "/api/projects/" + item.ID
	b := uiBinding{NodeID: "main", AssetID: projectAssetID(file), Kind: "render", Label: "粒子输出"}
	raw, _ := json.Marshal(b)
	w := plannerRequest(p, "POST", base+"/ui-preview", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, endpoint := range []string{"/workbench", "/app-ui"} {
		w = plannerRequest(p, "GET", base+endpoint, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var out struct{ UI projectUIView }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if out.UI.Total != 1 || out.UI.WithPreview != 1 || out.UI.WithCapture != 0 {
			t.Fatalf("coverage mismatch %s: %s", endpoint, w.Body.String())
		}
	}
	w = plannerRequest(p, "GET", base+"/ui-asset/"+b.AssetID, "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" {
		t.Fatal("cannot read local asset", w.Code)
	}
	b.Kind = "capture"
	raw, _ = json.Marshal(b)
	w = plannerRequest(p, "POST", base+"/ui-preview", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = plannerRequest(p, "DELETE", base+"/ui-preview?nodeId=main&assetId="+b.AssetID, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = plannerRequest(p, "GET", base+"/workbench", "")
	var state planState
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.UI.WithPreview != 0 {
		t.Fatal("removed binding still displayed")
	}
	w = plannerRequest(p, "POST", base+"/ui-preview", string(raw))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	putUIFixture(t, item, file, append(tinyPNG(), byte(0)))
	w = plannerRequest(p, "GET", base+"/workbench", "")
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.UI.WithPreview != 0 || len(state.UI.Warnings) == 0 {
		t.Fatal("changed image kept a silently stale association")
	}
	b.AssetID = "file-foreign"
	raw, _ = json.Marshal(b)
	if plannerRequest(p, "POST", base+"/ui-preview", string(raw)).Code != 404 {
		t.Fatal("foreign image accepted")
	}
}
func TestUICaptureUploadIsValidatedAndProjectScoped(t *testing.T) {
	p, item := uiFixture(t)
	base := "/api/projects/" + item.ID
	upload := func(data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("nodeId", "main")
		_ = form.WriteField("label", "原生页面截图")
		part, _ := form.CreateFormFile("file", "page.png")
		_, _ = part.Write(data)
		_ = form.Close()
		req := httptest.NewRequest("POST", base+"/ui-capture", &body)
		req.Header.Set("Content-Type", form.FormDataContentType())
		mux := http.NewServeMux()
		p.register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if w := upload(tinyPNG()[:33]); w.Code != 400 {
		t.Fatal("truncated PNG accepted", w.Code)
	}
	if w := upload([]byte("not an image")); w.Code != 400 {
		t.Fatal("invalid capture accepted", w.Code)
	}
	w := upload(tinyPNG())
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	w = plannerRequest(p, "GET", base+"/ui-asset/"+saved["assetId"], "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), tinyPNG()) {
		t.Fatal("stored capture differs")
	}
	w = plannerRequest(p, "GET", base+"/workbench", "")
	var state planState
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state.UI.WithCapture != 1 || state.UI.WithPreview != 1 {
		t.Fatal("capture did not reach graph")
	}
	other := t.TempDir()
	otherID := checksum(other)
	_, _ = p.db.Exec(`INSERT INTO projects VALUES(?,?,?)`, otherID, other, "")
	if plannerRequest(p, "GET", "/api/projects/"+otherID+"/ui-asset/"+saved["assetId"], "").Code != 404 {
		t.Fatal("capture leaked across projects")
	}
	if w = upload(tinyPNG()); w.Code != 200 {
		t.Fatal("duplicate capture should reuse asset", w.Code)
	}
	var count int
	_ = p.db.QueryRow(`SELECT count(*) FROM project_ui_captures WHERE project_id=?`, item.ID).Scan(&count)
	if count != 1 {
		t.Fatal("duplicate blob stored")
	}
}
func TestUIModelAssociationsRequireRealPageMediaAndEvidence(t *testing.T) {
	_, item := uiFixture(t)
	putUIFixture(t, item, "previews/native.png", tinyPNG())
	doc := samplePlan()
	doc.Nodes[0].Kind = "page"
	binding := uiBinding{NodeID: "main", File: "previews/native.png", Kind: "capture", Label: "界面", Evidence: []planEvidence{{File: "main.go", Match: "func main()"}}}
	valid, err := validateUIBindings(item, doc, []uiBinding{binding})
	if err != nil || len(valid) != 1 || valid[0].Revision == "" {
		t.Fatal(err, valid)
	}
	for _, file := range []string{"../private.png", "https://example.com/image.png", "previews/missing.png"} {
		bad := binding
		bad.File = file
		if _, err = validateUIBindings(item, doc, []uiBinding{bad}); err == nil {
			t.Fatal("bad media accepted", file)
		}
	}
	bad := binding
	bad.NodeID = "missing"
	if _, err = validateUIBindings(item, doc, []uiBinding{bad}); err == nil {
		t.Fatal("unknown page accepted")
	}
}

func TestLegacyPlanCannotInjectMediaOutsideItsSchema(t *testing.T) {
	doc, err := parsePlanReply(`{"nodes":[{"id":"page","preview":{"captures":[{"url":"https://outside.example/track"}]}}],"uiPreviews":[{"nodeId":"page","file":"previews/private.png","kind":"capture"}]}`)
	if err != nil || doc.Nodes[0].Preview != nil || len(doc.UIPreviews) != 0 {
		t.Fatal("unvalidated legacy media accepted", err)
	}
}

func TestAnalysisPreviewFailureDoesNotDiscardSourceProgress(t *testing.T) {
	p, item := uiFixture(t)
	putUIFixture(t, item, "previews/native.png", tinyPNG())
	a, err := p.refreshAnalysis(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	doc, _, _, err := p.document(item)
	if err != nil {
		t.Fatal(err)
	}
	reply := `{"nodes":[],"edges":[],"reviewedFiles":["main.go"],"uiPreviews":[{"nodeId":"main","file":"previews/missing.png","kind":"capture","label":"invalid","evidence":[{"file":"main.go","match":"func main()"}]}]}`
	next, coverage, err := applyAnalysisReply(item, doc, a, nil, turnOutput{Reply: reply}, map[string]bool{"main.go": true})
	if err != nil || coverage.view().Reviewed != 1 || len(next.UIPreviews) != 0 || len(next.Questions) == 0 {
		t.Fatal("bad preview discarded source progress", err)
	}
	reply = strings.ReplaceAll(reply, "previews/missing.png", "previews/native.png")
	next, _, err = applyAnalysisReply(item, next, coverage, nil, turnOutput{Reply: reply}, map[string]bool{"main.go": true})
	if err != nil || len(next.UIPreviews) != 1 || next.UIPreviews[0].Source != "codex" {
		t.Fatal("valid model binding not saved", err)
	}
	retained, _, err := applyAnalysisReply(item, next, coverage, nil, turnOutput{Reply: `{"nodes":[],"edges":[],"reviewedFiles":[],"uiPreviews":[]}`}, nil)
	if err != nil || len(retained.UIPreviews) != 1 {
		t.Fatal("later batch erased page preview", err)
	}
}
