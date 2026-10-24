package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const preProxyFixture = `name: KR-Oracle
type: vless
server: pre.example.com
port: 443
uuid: 12345678-1234-1234-1234-123456789abc
network: tcp
udp: true
tls: true
flow: xtls-rprx-vision
client-fingerprint: edge
reality-opts:
  public-key: sample-public-key
  short-id: ""
servername: example.com
`

func createTestPreProxy(t *testing.T, h http.Handler, name string) PreProxy {
	t.Helper()
	r := request(h, "POST", "/api/pre-proxies", map[string]any{"name": name, "content": preProxyFixture})
	if r.Code != http.StatusCreated {
		t.Fatalf("create pre-proxy: %d %s", r.Code, r.Body.String())
	}
	var st State
	if err := json.Unmarshal(r.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	return st.PreProxies[len(st.PreProxies)-1]
}

func seedPreProxyResults(n *Node) {
	now, latency := time.Now().UTC(), 72
	n.Latency, n.TestedAt, n.TestError = &latency, &now, "old test error"
	n.ExitIP, n.ExitIPCheckedAt, n.ExitIPError = "192.0.2.1", &now, "old IP error"
}

func assertPreProxyResultsCleared(t *testing.T, n Node) {
	t.Helper()
	if n.Latency != nil || n.TestedAt != nil || n.TestError != "" || n.ExitIP != "" || n.ExitIPCheckedAt != nil || n.ExitIPError != "" {
		t.Fatalf("node %s retained obsolete results", n.ID)
	}
}

func seedPreProxyNodes(t *testing.T, a *App) State {
	t.Helper()
	if err := a.store.Update(func(st *State) error {
		if err := reconcile(st, "sub", fixtureProxies(t)); err != nil {
			return err
		}
		disabled, missing := st.Nodes[0], st.Nodes[1]
		disabled.ID, disabled.Port, disabled.Enabled, disabled.SubscriptionID = "disabled", 20003, false, ""
		missing.ID, missing.Port, missing.Available = "missing", 20004, false
		st.Nodes = append(st.Nodes, disabled, missing)
		for i := range st.Nodes {
			seedPreProxyResults(&st.Nodes[i])
		}
		st.AppliedRevision = st.Revision
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return a.store.Snapshot()
}

func TestPreProxyCRUDRevisionsAndResultInvalidation(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	before := seedPreProxyNodes(t, a)
	p := createTestPreProxy(t, h, "  KR-Oracle  ")
	if p.ID == "" || p.Name != "KR-Oracle" || a.store.Snapshot().Revision != before.Revision {
		t.Fatal("create must trim display name and not change revision")
	}
	unused := createTestPreProxy(t, h, "Unused")
	updatedContent := strings.Replace(preProxyFixture, "port: 443", "port: 8443", 1)
	if r := request(h, "PUT", "/api/pre-proxies/"+unused.ID, map[string]any{"name": unused.Name, "content": updatedContent}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if r := request(h, "DELETE", "/api/pre-proxies/"+unused.ID, nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if a.store.Snapshot().Revision != before.Revision {
		t.Fatal("unused pre-proxy edits/deletion must not block node tests")
	}
	ids := []string{before.Nodes[0].ID, before.Nodes[2].ID, before.Nodes[3].ID}
	if r := request(h, "POST", "/api/nodes/batch-pre-proxy", map[string]any{"ids": ids, "preProxyId": p.ID}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if err := a.store.Update(func(st *State) error {
		for i := range st.Nodes {
			seedPreProxyResults(&st.Nodes[i])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bound := a.store.Snapshot()
	// JSON round-trip port is float64; equivalent YAML uses int. Neither that nor input name affects runtime config.
	config := bound.PreProxies[0].Config
	config["name"] = "another input name"
	content, _ := json.Marshal(config)
	if r := request(h, "PUT", "/api/pre-proxies/"+p.ID, map[string]any{"name": "New display name", "content": string(content)}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	renamed := a.store.Snapshot()
	if renamed.Revision != bound.Revision || !reflect.DeepEqual(renamed.Nodes, bound.Nodes) {
		t.Fatal("name-only/equivalent config edit changed revision or results")
	}
	if r := request(h, "PUT", "/api/pre-proxies/"+p.ID, map[string]any{"name": "New display name", "content": updatedContent}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	changed := a.store.Snapshot()
	if changed.Revision != bound.Revision+1 {
		t.Fatal("referenced config edit must increment revision once")
	}
	for i, n := range changed.Nodes {
		if i == 1 {
			if !reflect.DeepEqual(n, bound.Nodes[i]) {
				t.Fatal("unrelated node changed")
			}
			continue
		}
		assertPreProxyResultsCleared(t, n)
		if n.PreProxyID != p.ID || n.Port != before.Nodes[i].Port || n.Enabled != before.Nodes[i].Enabled || n.Available != before.Nodes[i].Available {
			t.Fatal("config edit changed mapping or status")
		}
	}
	if err := a.store.Update(func(st *State) error {
		for i := range st.Nodes {
			seedPreProxyResults(&st.Nodes[i])
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if r := request(h, "DELETE", "/api/pre-proxies/"+p.ID, nil); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	after := a.store.Snapshot()
	if len(after.PreProxies) != 0 || after.Revision != changed.Revision+1 {
		t.Fatal("delete did not remove proxy and increment revision once")
	}
	for i, n := range after.Nodes {
		if n.PreProxyID != "" || n.Port != before.Nodes[i].Port || n.Enabled != before.Nodes[i].Enabled || n.Available != before.Nodes[i].Available {
			t.Fatal("delete must only unbind, never bypass/remove original nodes")
		}
		if i != 1 {
			assertPreProxyResultsCleared(t, n)
		}
	}
	for _, event := range after.Events {
		if strings.Contains(event.Message, "sample-public-key") || strings.Contains(event.Message, "12345678-") {
			t.Fatal("configuration credentials leaked into events")
		}
	}
}

func TestPreProxyValidationAndRollback(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	p := createTestPreProxy(t, h, "Existing")
	for _, tc := range []struct{ name, content string }{
		{"", preProxyFixture}, {"  ", preProxyFixture}, {strings.Repeat("a", 201), preProxyFixture},
		{" Existing ", preProxyFixture}, {"Other", ""}, {"Other", fixture}, {"Other", "{"},
		{"Other", "{name: X, server: x, port: 443}"},
		{"Other", preProxyFixture + "dialer-proxy: nested\n"},
	} {
		before := a.store.Snapshot()
		r := request(h, "POST", "/api/pre-proxies", map[string]any{"name": tc.name, "content": tc.content})
		if r.Code != 400 || !reflect.DeepEqual(before, a.store.Snapshot()) {
			t.Fatalf("invalid create changed state or accepted input: %d %s", r.Code, r.Body.String())
		}
	}
	for _, port := range []string{"0", "-1", "65536", "443.5", "null", "true", "\"443\"", "[]", "{}", ".inf", ".nan"} {
		if _, err := parsePreProxy(strings.Replace(preProxyFixture, "port: 443", "port: "+port, 1)); err == nil {
			t.Errorf("accepted invalid port %s", port)
		}
	}
	for _, port := range []string{"1", "65535", "443.0"} {
		if _, err := parsePreProxy(strings.Replace(preProxyFixture, "port: 443", "port: "+port, 1)); err != nil {
			t.Errorf("rejected integer-valued port %s: %v", port, err)
		}
	}
	if _, err := parsePreProxy("socks5://user:password@pre.example.com:1080#Socks"); err != nil {
		t.Fatal("share-link parsing failed:", err)
	}
	other := createTestPreProxy(t, h, "Other")
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/pre-proxies/unknown", map[string]any{"name": "Missing", "content": preProxyFixture}},
		{"DELETE", "/api/pre-proxies/unknown", nil},
		{"PUT", "/api/pre-proxies/" + other.ID, map[string]any{"name": p.Name, "content": preProxyFixture}},
		{"PUT", "/api/pre-proxies/" + p.ID, map[string]any{"name": p.Name, "content": "{"}},
	} {
		before := a.store.Snapshot()
		if r := request(h, tc.method, tc.path, tc.body); r.Code != 400 || !reflect.DeepEqual(before, a.store.Snapshot()) {
			t.Fatalf("failed update was not atomic: %d %s", r.Code, r.Body.String())
		}
	}
}

func TestPreProxySingleNodePatchCompatibility(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	before := seedPreProxyNodes(t, a)
	p := createTestPreProxy(t, h, "First")
	id := before.Nodes[0].ID
	path := "/api/nodes/" + id
	if r := request(h, "PATCH", path, map[string]any{"preProxyId": p.ID}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	bound := a.store.Snapshot()
	assertPreProxyResultsCleared(t, bound.Nodes[0])
	if bound.Nodes[0].PreProxyID != p.ID || bound.Revision != before.Revision+1 {
		t.Fatal("single bind failed")
	}
	for _, body := range []any{
		map[string]any{"preProxyId": p.ID},
		map[string]any{"preProxyId": "missing", "name": "must rollback"},
		map[string]any{"preProxyId": "", "port": before.Nodes[1].Port},
		map[string]any{"preProxyId": "", "name": ""},
		map[string]any{"preProxyId": "", "yaml": preProxyFixture}, // subscription config remains read-only
	} {
		wantCode := 400
		if b := body.(map[string]any); len(b) == 1 {
			wantCode = 200
		}
		prev := a.store.Snapshot()
		if r := request(h, "PATCH", path, body); r.Code != wantCode || !reflect.DeepEqual(prev, a.store.Snapshot()) {
			t.Fatalf("PATCH no-op/rollback failed: %d %s", r.Code, r.Body.String())
		}
	}
	if r := request(h, "PATCH", path, map[string]any{"name": "Existing client rename"}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if a.store.Snapshot().Nodes[0].PreProxyID != p.ID {
		t.Fatal("omitted binding changed existing value")
	}
	if r := request(h, "POST", "/api/nodes/batch", map[string]any{"ids": []string{id}, "enabled": false}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if a.store.Snapshot().Nodes[0].PreProxyID != p.ID || a.store.Snapshot().Nodes[0].Enabled {
		t.Fatal("legacy batch enable/disable changed binding")
	}
	if r := request(h, "PATCH", path, map[string]any{"preProxyId": ""}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if a.store.Snapshot().Nodes[0].PreProxyID != "" {
		t.Fatal("single unbind failed")
	}
}

func TestPreProxyBatchAtomicSelectionAndNoOp(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	before := seedPreProxyNodes(t, a)
	p := createTestPreProxy(t, h, "Batch")
	id := before.Nodes[0].ID
	for _, body := range []any{
		map[string]any{"ids": []string{}, "preProxyId": p.ID},
		map[string]any{"ids": []string{id}},
		map[string]any{"ids": []string{id}, "preProxyId": nil},
		map[string]any{"ids": []string{id}, "preProxyId": 1},
		map[string]any{"ids": []string{id}, "preProxyId": "missing"},
		map[string]any{"ids": []string{id, "deleted"}, "preProxyId": p.ID},
	} {
		prev := a.store.Snapshot()
		disk, _ := os.ReadFile(filepath.Join(a.store.dir, "state.json"))
		r := request(h, "POST", "/api/nodes/batch-pre-proxy", body)
		afterDisk, _ := os.ReadFile(filepath.Join(a.store.dir, "state.json"))
		if r.Code != 400 || !reflect.DeepEqual(prev, a.store.Snapshot()) || !bytes.Equal(disk, afterDisk) {
			t.Fatalf("invalid batch partially committed: %d %s", r.Code, r.Body.String())
		}
	}
	ids := []string{id, id, before.Nodes[2].ID}
	body := map[string]any{"ids": ids, "preProxyId": p.ID}
	prev := a.store.Snapshot()
	if r := request(h, "POST", "/api/nodes/batch-pre-proxy", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	bound := a.store.Snapshot()
	if bound.Revision != prev.Revision+1 || len(bound.Events) != len(prev.Events)+1 {
		t.Fatal("batch incremented revision/event count incorrectly")
	}
	for i, n := range bound.Nodes {
		if i == 0 || i == 2 {
			if n.PreProxyID != p.ID {
				t.Fatal("selected node not bound")
			}
			assertPreProxyResultsCleared(t, n)
		} else if !reflect.DeepEqual(n, prev.Nodes[i]) {
			t.Fatal("unselected node changed")
		}
	}
	if r := request(h, "POST", "/api/nodes/batch-pre-proxy", body); r.Code != 200 || !reflect.DeepEqual(bound, a.store.Snapshot()) {
		t.Fatal("no-op batch changed revision, events or results")
	}
	body["preProxyId"] = ""
	if r := request(h, "POST", "/api/nodes/batch-pre-proxy", body); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	if after := a.store.Snapshot(); after.Revision != bound.Revision+1 || after.Nodes[0].PreProxyID != "" || after.Nodes[2].PreProxyID != "" {
		t.Fatal("batch unbind failed")
	}
}

func TestPreProxyGeneratedConfigAndExactRouting(t *testing.T) {
	st := initialState()
	if err := reconcile(&st, "sub", fixtureProxies(t)); err != nil {
		t.Fatal(err)
	}
	baseline, err := GenerateConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	linksBefore, _ := ExportLinks(st, "http", nil)
	pre, err := parsePreProxy(preProxyFixture)
	if err != nil {
		t.Fatal(err)
	}
	st.PreProxies = []PreProxy{{ID: "shared", Name: "Display", Config: pre}, {ID: "unused", Name: "Unused", Config: pre}}
	unbound, _ := GenerateConfig(st)
	if !bytes.Equal(baseline, unbound) {
		t.Fatal("unreferenced proxies changed legacy config output")
	}
	for i := range st.Nodes {
		st.Nodes[i].PreProxyID = "shared"
	}
	before := clone(st)
	data, err := GenerateConfig(st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, clone(st)) {
		t.Fatal("generation mutated source configs")
	}
	var doc, original struct {
		Proxies   []map[string]any `yaml:"proxies"`
		Listeners []map[string]any `yaml:"listeners"`
		Rules     []string         `yaml:"rules"`
	}
	if yaml.Unmarshal(data, &doc) != nil || yaml.Unmarshal(baseline, &original) != nil {
		t.Fatal("invalid generated YAML")
	}
	if len(doc.Proxies) != 3 || len(doc.Listeners) != 2 || !reflect.DeepEqual(doc.Listeners, original.Listeners) || !reflect.DeepEqual(doc.Rules, original.Rules) {
		t.Fatal("extra listeners, duplicate/unused pre-proxies, or changed routing/authentication")
	}
	if doc.Proxies[0]["name"] != "pre-shared" || !reflect.DeepEqual(doc.Proxies[0]["reality-opts"], pre["reality-opts"]) {
		t.Fatal("pre-proxy name or nested config lost")
	}
	for i, n := range st.Nodes {
		if doc.Proxies[i+1]["name"] != configName(n) || doc.Proxies[i+1]["dialer-proxy"] != "pre-shared" {
			t.Fatal("incorrect node proxy chain")
		}
	}
	for range 5 {
		next, _ := GenerateConfig(st)
		if !bytes.Equal(data, next) {
			t.Fatal("configuration output not deterministic")
		}
	}
	linksAfter, _ := ExportLinks(st, "http", nil)
	if linksBefore != linksAfter {
		t.Fatal("pre-proxy changed exported node endpoints")
	}
	st.Nodes[0].Enabled, st.Nodes[1].Available = false, false
	data, err = GenerateConfig(st)
	if err != nil || yaml.Unmarshal(data, &doc) != nil || len(doc.Proxies) != 0 || len(doc.Listeners) != 0 {
		t.Fatal("inactive-only bindings generated runtime proxies")
	}
	for _, tc := range []struct{ enabled, available bool }{{true, true}, {false, true}, {true, false}} {
		st.Nodes[0].Enabled, st.Nodes[0].Available, st.Nodes[0].PreProxyID = tc.enabled, tc.available, "unknown"
		if _, err := GenerateConfig(st); err == nil {
			t.Fatal("invalid reference silently fell back to direct connection")
		}
	}
}

func TestPreProxyPersistenceAndSubscriptionRefresh(t *testing.T) {
	s := newStore(t)
	old := s.Snapshot()
	data, _ := json.Marshal(old)
	var legacy map[string]any
	_ = json.Unmarshal(data, &legacy)
	delete(legacy, "preProxies")
	data, _ = json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(s.dir, "state.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(s.dir)
	if err != nil || len(s.Snapshot().PreProxies) != 0 || s.Snapshot().Version != 1 || s.Snapshot().Settings.Password != old.Settings.Password {
		t.Fatal("old state failed to load without migration")
	}
	proxies := fixtureProxies(t)
	pre, _ := parsePreProxy(preProxyFixture)
	if err := s.Update(func(st *State) error {
		if err := reconcile(st, "sub", proxies); err != nil {
			return err
		}
		st.PreProxies = []PreProxy{{ID: "persistent", Name: "Persistent", Config: pre}}
		st.Nodes[0].PreProxyID = "persistent"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := s.Snapshot()
	reopened, err := OpenStore(s.dir)
	if err != nil || !reflect.DeepEqual(before, reopened.Snapshot()) {
		t.Fatal("saved proxy and binding lost after reopening")
	}
	for _, input := range [][]map[string]any{proxies[1:], {proxies[1], proxies[0]}} {
		if err := reopened.Update(func(st *State) error { return reconcile(st, "sub", input) }); err != nil {
			t.Fatal(err)
		}
		n := reopened.Snapshot().Nodes[0]
		if n.ID != before.Nodes[0].ID || n.Port != before.Nodes[0].Port || n.PreProxyID != "persistent" {
			t.Fatal("subscription refresh lost user binding/port")
		}
	}
}

func TestPreProxyNewRoutesRequireAuthentication(t *testing.T) {
	_, h := testHandler(t, "long-admin-password")
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/pre-proxies"}, {"PUT", "/api/pre-proxies/id"},
		{"DELETE", "/api/pre-proxies/id"}, {"POST", "/api/nodes/batch-pre-proxy"},
	} {
		if r := request(h, tc.method, tc.path, map[string]any{}); r.Code != 401 {
			t.Fatalf("unauthenticated new route allowed: %s %s", tc.method, tc.path)
		}
	}
}

func TestPreProxyUnusedCreationDoesNotBlockNodeTests(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/delay") {
			respond(w, 200, map[string]int{"delay": 42})
			return
		}
		if r.URL.Path == "/version" {
			respond(w, 200, map[string]string{"version": "mock"})
			return
		}
		respond(w, 200, map[string]any{"connections": []any{}})
	}))
	defer controller.Close()
	seedPreProxyNodes(t, a)
	if err := a.store.Update(func(st *State) error { st.Settings.ControllerURL = controller.URL; return nil }); err != nil {
		t.Fatal(err)
	}
	before := a.store.Snapshot()
	createTestPreProxy(t, h, "Not used")
	if r := request(h, "POST", "/api/nodes/test", map[string]any{"ids": []string{before.Nodes[0].ID}}); r.Code != 200 {
		t.Fatalf("unused proxy blocked test: %d %s", r.Code, r.Body.String())
	}
}

func TestPreProxyApplyPersistsOnlyAcceptedChain(t *testing.T) {
	a, h := authenticatedTestHandler(t)
	before := seedPreProxyNodes(t, a)
	p := createTestPreProxy(t, h, "Chain")
	if r := request(h, "PATCH", "/api/nodes/"+before.Nodes[0].ID, map[string]any{"preProxyId": p.ID}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	var reject atomic.Bool
	controller := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Payload string `json:"payload"`
		}
		if r.Method != "PUT" || r.URL.Path != "/configs" || r.URL.Query().Get("force") != "true" || json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Error("invalid apply request")
		}
		if !strings.Contains(body.Payload, "dialer-proxy: "+preProxyConfigName(p.ID)) || !strings.Contains(body.Payload, "name: "+preProxyConfigName(p.ID)) {
			t.Error("applied payload missing chain")
		}
		if reject.Load() {
			w.WriteHeader(400)
			return
		}
		w.WriteHeader(204)
	}))
	defer controller.Close()
	if err := a.store.Update(func(st *State) error { st.Settings.ControllerURL = controller.URL; return nil }); err != nil {
		t.Fatal(err)
	}
	preview := request(h, "GET", "/api/config", nil)
	if preview.Code != 200 {
		t.Fatal(preview.Body.String())
	}
	if r := request(h, "POST", "/api/core/apply", map[string]any{}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	applied := a.store.Snapshot()
	disk, err := os.ReadFile(filepath.Join(a.store.dir, "mihomo.yaml"))
	if err != nil || !bytes.Equal(disk, preview.Body.Bytes()) || applied.Revision != applied.AppliedRevision {
		t.Fatal("accepted config not persisted/applied")
	}
	updatedContent := strings.Replace(preProxyFixture, "port: 443", "port: 8443", 1)
	if r := request(h, "PUT", "/api/pre-proxies/"+p.ID, map[string]any{"name": p.Name, "content": updatedContent}); r.Code != 200 {
		t.Fatal(r.Body.String())
	}
	reject.Store(true)
	if r := request(h, "POST", "/api/core/apply", map[string]any{}); r.Code != 400 {
		t.Fatal("rejected config reported success")
	}
	after := a.store.Snapshot()
	afterDisk, _ := os.ReadFile(filepath.Join(a.store.dir, "mihomo.yaml"))
	if after.AppliedRevision != applied.AppliedRevision || after.Revision == after.AppliedRevision || !bytes.Equal(disk, afterDisk) {
		t.Fatal("rejected chain replaced accepted runtime config")
	}
}
