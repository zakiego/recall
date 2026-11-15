package connectors_test

// connectors.test.ts: the Gmail connector against the fake Google. A
// connection is made with CompleteConnect (the callback's work after its
// cookie and session checks); the HTTP start and callback cases, Better
// Auth sign-in and the REST import route are ported with the routes (M5,
// M7).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zakiego/recall/apps/server/internal/connectors"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/jobs"
	"github.com/zakiego/recall/apps/server/internal/logx"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/pipeline"
	"github.com/zakiego/recall/apps/server/internal/testkit"
	"github.com/zakiego/recall/apps/server/internal/testutil"
	"github.com/zakiego/recall/apps/server/internal/users"
)

const (
	ticket  = "18c2a1f0b9e4d701"
	kickoff = "18c29e7d3a5b6c02"
	bill    = "18c29b11e0f4a903"
	invoice = "18c2950a7f1c3d05"
	pw      = "correct horse battery staple"
)

// Strings that exist only inside fixture mail or tokens. None may reach a log.
var secretWords = []string{"KX7Q2M", "Banjarbaru", "412.500", "Rina owns", "fake-refresh", "fake-access", "INV-2026-1107"}

func TestMain(m *testing.M) {
	embed.HubURL = "http://127.0.0.1:1/"
	for k, v := range map[string]string{
		"GOOGLE_CLIENT_ID": "test-client.apps.googleusercontent.com", "GOOGLE_CLIENT_SECRET": "test-client-secret",
		"CONNECTOR_SECRET": "test-connector-secret-0123456789abcdefghijklmnop", "RECALL_URL": "http://localhost:4800",
		"GATEWAY_KEY": "zkg_test", "GATEWAY_URL": "http://gateway.test", "RECALL_EMBED": "inline", "MODELS_DIR": testutil.ModelsDir(),
	} {
		os.Setenv(k, v)
	}
	if os.Getenv("ONNXRUNTIME_LIB") == "" {
		p, err := embed.DefaultLibPath(testutil.Root())
		if err != nil {
			panic(err)
		}
		os.Setenv("ONNXRUNTIME_LIB", p)
	}
	os.Exit(m.Run())
}

type world struct {
	d      *db.DB
	a, b   string
	ids    map[string]string
	ctx    context.Context
	logOut *bytes.Buffer
}

func setup(t *testing.T) *world {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	d, err := db.Open(filepath.Join(dir, "recall.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	w := &world{d: d, ids: map[string]string{}, ctx: context.Background()}
	wa, _ := users.CreateWorkspace(d, "Alpha", "", "")
	wb, _ := users.CreateWorkspace(d, "Beta", "", "")
	w.a, w.b = wa.ID, wb.ID
	for _, x := range [][3]string{{"zaki", w.a, "owner"}, {"eva", w.a, "admin"}, {"budi", w.a, "member"}, {"mallory", w.b, "owner"}} {
		u, err := users.Insert(d, users.NewUser{Username: x[0], PasswordHash: "unused"})
		if err != nil {
			t.Fatal(err)
		}
		w.ids[x[0]] = u.ID
		users.AddMember(d, x[1], u.ID, x[2])
	}
	return w
}

func (w *world) connect(t *testing.T, name string, fake *connectors.FakeGoogle) string {
	t.Helper()
	ws := w.a
	if name == "mallory" {
		ws = w.b
	}
	return connectors.CompleteConnect(w.ctx, w.d, ws, w.ids[name], "4/0Afake", "verifier", fake.Do, time.Now().UnixMilli())
}

func (w *world) drain(t *testing.T, fake *connectors.FakeGoogle) {
	g := &testkit.Gateway{Default: `{"add":[]}`}
	r := jobs.NewRunner(w.d, pipeline.Handlers(w.d, pipeline.Deps{Do: g.Do, Now: func() time.Time { return time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC) }, GmailDo: fake.Do}), func(m string) { logx.Info("[recall:jobs] %s", m) })
	for i := 0; i < 50; i++ {
		j, err := r.RunOnce(time.Now().UnixMilli() + 3_600_000)
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			return
		}
	}
}

func (w *world) docs(t *testing.T, sourceID string) []model.Document {
	rows, err := w.d.Query(`SELECT `+model.DocumentCols+` FROM documents WHERE source_id = ?`, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []model.Document
	for rows.Next() {
		doc, _ := model.ScanDocument(rows)
		out = append(out, doc)
	}
	return out
}

func (w *world) count(t *testing.T, table string) int {
	var n int
	w.d.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
	return n
}

func (w *world) search(name, ws, container, q string, fake *connectors.FakeGoogle) (connectors.SearchResult, error) {
	return connectors.Search(w.ctx, w.d, connectors.SearchInput{WorkspaceID: ws, UserID: w.ids[name], Container: container, Q: q}, fake.Do)
}

func (w *world) imp(name, container string, msgIDs ...string) (connectors.ImportResult, error) {
	var msgs []connectors.ImportMessage
	for _, id := range msgIDs {
		msgs = append(msgs, connectors.ImportMessage{ID: id})
	}
	return connectors.Import(w.d, connectors.ImportInput{WorkspaceID: w.a, UserID: w.ids[name], Container: container, Messages: msgs})
}

func code(err error) string {
	var ce *connectors.ConnectorError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func meta(doc model.Document, k string) any {
	if doc.Metadata == nil {
		return nil
	}
	v, _ := doc.Metadata.Get(k)
	return v
}

func content(doc model.Document) string {
	if doc.Content == nil {
		return ""
	}
	return *doc.Content
}

// --- sealed values -----------------------------------------------------------------

func TestSealedValues(t *testing.T) {
	secret := "test-connector-secret-0123456789abcdefghijklmnop"
	sealed, err := connectors.Seal(secret, `{"refreshToken":"abc"}`, "connector:con_1:ws:usr")
	if err != nil || strings.Contains(sealed, "abc") {
		t.Fatal(sealed, err)
	}
	if v, err := connectors.Unseal(secret, sealed, "connector:con_1:ws:usr"); err != nil || v != `{"refreshToken":"abc"}` {
		t.Fatal(v, err)
	}
	var se *connectors.SealError
	if _, err := connectors.Unseal(secret, sealed, "connector:con_2:ws:usr"); !errors.As(err, &se) {
		t.Fatal(err)
	}
	if _, err := connectors.Unseal(secret+"x", sealed, "connector:con_1:ws:usr"); !errors.As(err, &se) {
		t.Fatal(err)
	}
	p := strings.Split(sealed, ".")
	flip := "A"
	if p[2][0] == 'A' {
		flip = "B"
	}
	p[2] = flip + p[2][1:]
	if _, err := connectors.Unseal(secret, strings.Join(p, "."), "connector:con_1:ws:usr"); !errors.As(err, &se) {
		t.Fatal(err)
	}
	if _, err := connectors.Seal("short", "x", "c"); !errors.As(err, &se) {
		t.Fatal(err)
	}
}

// --- parseMessage ------------------------------------------------------------------

func fixture(id string) connectors.GmailMessage {
	for _, m := range connectors.Fixtures().Messages {
		if m.ID == id {
			return m
		}
	}
	panic(id)
}

func TestParseFixtures(t *testing.T) {
	p, _ := connectors.ParseMessage(fixture(ticket))
	if *p.Subject != "E-ticket: Jakarta to Banjarbaru, 14 Nov" || !strings.HasPrefix(p.Text, "Subject: E-ticket") || !strings.Contains(p.Text, "From: Garuda Indonesia") ||
		!strings.Contains(p.Text, "Booking code: KX7Q2M") || strings.Contains(p.Text, "\r") || len(p.Pdfs) != 1 ||
		p.Pdfs[0].PartID != "2" || *p.Pdfs[0].AttachmentID != "ANGjdJ8w-ticket-part-2" || p.Pdfs[0].Filename != "e-ticket-GA532.pdf" {
		t.Fatalf("%+v", p)
	}
	b, _ := connectors.ParseMessage(fixture(bill))
	if !strings.Contains(b.Text, "Rp 412.500") || strings.Contains(b.Text, "<p>") || strings.Contains(b.Text, "color:red") {
		t.Fatal(b.Text)
	}
	inv, _ := connectors.ParseMessage(fixture(invoice))
	if len(inv.Pdfs) != 1 || inv.Pdfs[0].PartID != "1" || inv.Pdfs[0].AttachmentID != nil || inv.Pdfs[0].Filename != "INV-2026-1107.pdf" || inv.Pdfs[0].InlineData == nil || *inv.Pdfs[0].InlineData == "" {
		t.Fatalf("%+v", inv.Pdfs)
	}
	if got := connectors.DecodeEntities("a &amp; b &#39;c&#39; &lt;d&gt; &#x41;"); got != "a & b 'c' <d> A" {
		t.Fatal(got)
	}
}

// --- configuration -----------------------------------------------------------------

func TestConfiguration(t *testing.T) {
	w := setup(t)
	t.Setenv("GOOGLE_CLIENT_ID", "")
	t.Setenv("CONNECTOR_SECRET", "too-short")
	if connectors.Configured() || strings.Join(connectors.MissingKeys(), ",") != "GOOGLE_CLIENT_ID,CONNECTOR_SECRET" {
		t.Fatal(connectors.MissingKeys())
	}
	if _, err := w.imp("zaki", "inbox", ticket); err == nil || !strings.Contains(err.Error(), "isn't set up") {
		t.Fatal(err)
	}
}

func TestReconnectRevokesOldGrant(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.connect(t, "zaki", fake)
	if strings.Join(fake.RevokedTokens, ",") != "fake-refresh-1" || w.count(t, "connector_accounts") != 1 {
		t.Fatal(fake.RevokedTokens)
	}
}

// --- search and import ---------------------------------------------------------------

func ids(r connectors.SearchResult) []string {
	var out []string
	for _, m := range r.Messages {
		out = append(out, m.ID)
	}
	return out
}

func imported(r connectors.SearchResult) []bool {
	var out []bool
	for _, m := range r.Messages {
		out = append(out, m.Imported)
	}
	return out
}

func TestSearchMarksImported(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	bills, err := w.search("zaki", w.a, "inbox", "label:bills", fake)
	if err != nil || strings.Join(ids(bills), ",") != bill+","+invoice {
		t.Fatal(ids(bills), err)
	}
	if m := bills.Messages[0]; m.From != "PLN Mobile <tagihan@pln.example>" || m.Subject != "Tagihan listrik Oktober 2026" || m.Imported {
		t.Fatalf("%+v", m)
	}
	gh, _ := w.search("zaki", w.a, "inbox", "from:github", fake)
	if !strings.Contains(gh.Messages[0].Snippet, "Tailwind classes & drop") {
		t.Fatal(gh.Messages[0].Snippet)
	}
	w.imp("zaki", "inbox", bill)
	again, _ := w.search("zaki", w.a, "inbox", "label:bills", fake)
	if fmtBools(imported(again)) != "true,false" {
		t.Fatal(imported(again))
	}
	// Imported is per container.
	other, _ := w.search("zaki", w.a, "work", "label:bills", fake)
	if fmtBools(imported(other)) != "false,false" {
		t.Fatal(imported(other))
	}
}

func fmtBools(b []bool) string {
	var s []string
	for _, x := range b {
		if x {
			s = append(s, "true")
		} else {
			s = append(s, "false")
		}
	}
	return strings.Join(s, ",")
}

func TestImportThroughJobLoop(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	subject := "E-ticket"
	first, err := connectors.Import(w.d, connectors.ImportInput{WorkspaceID: w.a, UserID: w.ids["zaki"], Container: "inbox", Messages: []connectors.ImportMessage{{ID: ticket, Subject: &subject}, {ID: kickoff}}})
	if err != nil || first.Queued != 2 || first.Duplicates != 0 {
		t.Fatal(first, err)
	}
	// Nothing was fetched from Gmail during the request itself.
	for _, c := range fake.Calls {
		if strings.Contains(c.URL, "/messages/"+ticket) {
			t.Fatal("fetched during import")
		}
	}
	if d := w.docs(t, "gmail:"+ticket)[0]; d.Status != "queued" || d.SourceType != "email" {
		t.Fatalf("%+v", d)
	}
	w.drain(t, fake)
	email := w.docs(t, "gmail:"+ticket)[0]
	if email.Status != "done" || *email.Title != "E-ticket: Jakarta to Banjarbaru, 14 Nov" || email.Container != "inbox" || email.WorkspaceID != w.a ||
		!strings.Contains(content(email), "Booking code: KX7Q2M") || meta(email, "connector") != "gmail" || meta(email, "messageId") != ticket || meta(email, "attachments") != float64(1) {
		t.Fatalf("%+v %v", email, email.Error)
	}
	pdf := w.docs(t, "gmail:"+ticket+":2")[0]
	if pdf.Status != "done" || pdf.SourceType != "file" || *pdf.Title != "e-ticket-GA532.pdf" || !strings.Contains(content(pdf), "KX7Q2M") {
		t.Fatalf("%+v %v", pdf, pdf.Error)
	}
	if k := w.docs(t, "gmail:"+kickoff)[0]; k.Status != "done" || *k.Title != "Re: Project kickoff notes" {
		t.Fatalf("%+v", k)
	}
	// The PNG logo is not a document.
	if w.count(t, "documents") != 3 {
		t.Fatal(w.count(t, "documents"))
	}
	again, _ := w.imp("zaki", "inbox", ticket, kickoff)
	if again.Queued != 0 || again.Duplicates != 2 {
		t.Fatal(again)
	}
	w.drain(t, fake)
	list, _ := connectors.ListImports(w.d, w.a, "inbox", w.ids["zaki"], 0)
	var sids []string
	for _, v := range list {
		sids = append(sids, *v.SourceID)
	}
	sort.Strings(sids)
	want := []string{"gmail:" + kickoff, "gmail:" + ticket, "gmail:" + ticket + ":2"}
	sort.Strings(want)
	if w.count(t, "documents") != 3 || strings.Join(sids, ",") != strings.Join(want, ",") {
		t.Fatal(sids)
	}
}

func TestInlinePDFAndHTMLOnly(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.imp("zaki", "inbox", invoice, bill)
	w.drain(t, fake)
	if d := w.docs(t, "gmail:"+invoice+":1"); len(d) != 1 || d[0].Status != "done" || d[0].SourceType != "file" {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(content(w.docs(t, "gmail:"+bill)[0]), "Jatuh tempo 20 November 2026") {
		t.Fatal("bill text")
	}
}

func TestRejectsBadIDsAndTooMany(t *testing.T) {
	w := setup(t)
	w.connect(t, "zaki", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	if _, err := w.imp("zaki", "inbox", "../../x"); code(err) != "invalid" {
		t.Fatal(err)
	}
	var many []string
	for i := 0; i < 51; i++ {
		many = append(many, fmt.Sprintf("18c2%012d", i))
	}
	if _, err := w.imp("zaki", "inbox", many...); err == nil || !strings.Contains(err.Error(), "up to 50") {
		t.Fatal(err)
	}
}

func TestRefreshesExpiredToken(t *testing.T) {
	w := setup(t)
	zero := 0.0
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{ExpiresIn: &zero})
	w.connect(t, "zaki", fake)
	before, _, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"])
	if _, err := w.search("zaki", w.a, "inbox", "", fake); err != nil {
		t.Fatal(err)
	}
	after, _, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"])
	refreshes := 0
	for _, c := range fake.Calls {
		if c.Body != nil && strings.Contains(*c.Body, "grant_type=refresh_token") {
			refreshes++
		}
	}
	if after.Tokens == before.Tokens || strings.Contains(after.Tokens, "fake-") || refreshes != 1 {
		t.Fatal(refreshes)
	}
}

func TestReconnectWhenRevoked(t *testing.T) {
	w := setup(t)
	zero := 0.0
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{ExpiresIn: &zero})
	w.connect(t, "zaki", fake)
	fake.Revoked = true
	if _, err := w.search("zaki", w.a, "inbox", "", fake); code(err) != "reauth" {
		t.Fatal(err)
	}
	if c, _, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"]); c.Status != "reauth" {
		t.Fatal(c.Status)
	}
	if _, err := w.imp("zaki", "inbox", ticket); err == nil || !strings.Contains(err.Error(), "Connect again") {
		t.Fatal(err)
	}
}

func TestDisconnectThenQueuedImportFails(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.imp("zaki", "inbox", ticket)
	res, err := connectors.Disconnect(w.ctx, w.d, w.a, w.ids["zaki"], fake.Do)
	if err != nil || res != (connectors.DisconnectResult{Removed: true, Revoked: true}) || strings.Join(fake.RevokedTokens, ",") != "fake-refresh-1" {
		t.Fatal(res, err, fake.RevokedTokens)
	}
	if _, found, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"]); found {
		t.Fatal("still connected")
	}
	w.drain(t, fake)
	doc := w.docs(t, "gmail:"+ticket)[0]
	if doc.Status != "failed" || *doc.Error != "the Gmail connection used for this import was removed" {
		t.Fatalf("%+v", doc)
	}
	// After connecting again, search flags it as failed and importing again retries it.
	w.connect(t, "zaki", fake)
	found, _ := w.search("zaki", w.a, "inbox", "from:garuda", fake)
	if m := found.Messages[0]; m.ID != ticket || !m.Imported || !m.ImportFailed {
		t.Fatalf("%+v", m)
	}
	if r, _ := w.imp("zaki", "inbox", ticket); r.Queued != 1 || r.Duplicates != 0 {
		t.Fatal(r)
	}
	w.drain(t, fake)
	after := w.docs(t, "gmail:"+ticket)
	if len(after) != 1 || after[0].ID != doc.ID || after[0].Status != "done" || after[0].Error != nil {
		t.Fatalf("%+v", after)
	}
}

func TestReconnectDoesntStrandImports(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.imp("zaki", "inbox", kickoff)
	w.connect(t, "zaki", fake)
	w.drain(t, fake)
	if d := w.docs(t, "gmail:"+kickoff)[0]; d.Status != "done" {
		t.Fatalf("%+v", d)
	}
}

// --- isolation -------------------------------------------------------------------------

func TestOnlyConnectingUserUsesConnection(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	for _, who := range []string{"eva", "budi"} {
		if _, err := w.search(who, w.a, "inbox", "", fake); code(err) != "not_connected" {
			t.Fatal(err)
		}
		if _, err := w.imp(who, "inbox", ticket); err == nil || !strings.Contains(err.Error(), "Connect Gmail first") {
			t.Fatal(err)
		}
	}
	seen, _ := connectors.ListConnections(w.d, w.a, w.ids["eva"])
	if len(seen) != 1 || *seen[0].Username != "zaki" || seen[0].Mine || seen[0].AccountEmail != nil || seen[0].Status != "active" {
		t.Fatalf("%+v", seen)
	}
	mine, _ := connectors.ListConnections(w.d, w.a, w.ids["zaki"])
	if !mine[0].Mine || *mine[0].AccountEmail != "zaki@example.com" {
		t.Fatalf("%+v", mine[0])
	}
}

func TestNeverCrossesWorkspaces(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	if _, err := w.search("mallory", w.b, "inbox", "", fake); code(err) != "not_connected" {
		t.Fatal(err)
	}
	// Zaki isn't in B, and his A connection doesn't follow him there.
	if _, err := w.search("zaki", w.b, "inbox", "", fake); code(err) != "not_connected" {
		t.Fatal(err)
	}
	if l, _ := connectors.ListConnections(w.d, w.b, w.ids["mallory"]); len(l) != 0 {
		t.Fatal(l)
	}
	w.imp("zaki", "inbox", ticket)
	if l, _ := connectors.ListImports(w.d, w.b, "inbox", w.ids["mallory"], 0); len(l) != 0 {
		t.Fatal(l)
	}
	// A document in B that points at A's connection must not read A's mail.
	conn, _, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"])
	w.d.Exec(`INSERT INTO documents (id, workspace_id, container, source_type, metadata) VALUES ('doc_forged', ?, 'inbox', 'email', ?)`, w.b,
		`{"connector":"gmail","messageId":"`+ticket+`","connectionId":"`+conn.ID+`","userId":"`+w.ids["zaki"]+`"}`)
	row := w.d.QueryRow(`SELECT ` + model.DocumentCols + ` FROM documents WHERE id = 'doc_forged'`)
	doc, err := model.ScanDocument(row)
	if err != nil {
		t.Fatal(err)
	}
	calls := len(fake.Calls)
	if _, _, _, err := connectors.ResolveEmail(w.ctx, w.d, doc, fake.Do); err == nil || !strings.Contains(err.Error(), "was removed") {
		t.Fatal(err)
	}
	if len(fake.Calls) != calls {
		t.Fatal("read A's mail")
	}
}

func TestQueuedImportNeverBorrowsAGrant(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.imp("zaki", "inbox", ticket)
	connectors.Disconnect(w.ctx, w.d, w.a, w.ids["zaki"], fake.Do)
	// Eva is connected in the same workspace; Zaki's import must not use her grant.
	w.connect(t, "eva", fake)
	// Zaki reconnects with a different Google account.
	w.connect(t, "zaki", connectors.NewFakeGoogle(connectors.FakeOptions{Email: "zaki.other@example.com"}))
	calls := len(fake.Calls)
	w.drain(t, fake)
	doc := w.docs(t, "gmail:"+ticket)[0]
	if doc.Status != "failed" || *doc.Error != "the Gmail connection used for this import was removed" {
		t.Fatalf("%+v", doc)
	}
	for _, c := range fake.Calls[calls:] {
		if strings.Contains(c.URL, "/messages/") {
			t.Fatal("read mail with another grant")
		}
	}
}

func TestOnlyImporterRetriesUnreadEmail(t *testing.T) {
	w := setup(t)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.imp("zaki", "inbox", ticket)
	doc := w.docs(t, "gmail:"+ticket)[0]
	if !connectors.MayRetry(doc.SourceType, doc.Content, doc.Metadata, w.ids["zaki"]) || connectors.MayRetry(doc.SourceType, doc.Content, doc.Metadata, w.ids["eva"]) {
		t.Fatal("mayRetry before ingest")
	}
	if l, _ := connectors.ListImports(w.d, w.a, "inbox", w.ids["eva"], 0); l[0].CanRetry {
		t.Fatal("eva can retry")
	}
	w.drain(t, fake)
	// Once its text is stored, a retry doesn't touch Gmail, so anyone may.
	for _, sid := range []string{"gmail:" + ticket, "gmail:" + ticket + ":2"} {
		d := w.docs(t, sid)[0]
		if !connectors.MayRetry(d.SourceType, d.Content, d.Metadata, w.ids["eva"]) {
			t.Fatal(sid)
		}
	}
}

func TestLeavingDropsConnection(t *testing.T) {
	w := setup(t)
	w.connect(t, "budi", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	if _, found, _ := connectors.GetConnection(w.d, w.a, w.ids["budi"]); !found {
		t.Fatal("not connected")
	}
	w.d.Exec(`DELETE FROM member WHERE user_id = ?`, w.ids["budi"])
	if _, found, _ := connectors.GetConnection(w.d, w.a, w.ids["budi"]); found {
		t.Fatal("connection survived")
	}
}

func TestTokenBoundToItsRow(t *testing.T) {
	w := setup(t)
	w.connect(t, "zaki", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	w.connect(t, "eva", connectors.NewFakeGoogle(connectors.FakeOptions{}))
	z, _, _ := connectors.GetConnection(w.d, w.a, w.ids["zaki"])
	e, _, _ := connectors.GetConnection(w.d, w.a, w.ids["eva"])
	e.Tokens = z.Tokens
	var se *connectors.SealError
	if _, err := connectors.AccessToken(w.ctx, w.d, e, connectors.NewFakeGoogle(connectors.FakeOptions{}).Do, time.Now().UnixMilli(), false); !errors.As(err, &se) {
		t.Fatal(err)
	}
}

// --- logs ----------------------------------------------------------------------------------

func TestLogsCarryNoMailOrTokens(t *testing.T) {
	w := setup(t)
	var buf bytes.Buffer
	logx.SetOutput(&buf, &buf)
	defer logx.SetOutput(os.Stdout, os.Stderr)
	fake := connectors.NewFakeGoogle(connectors.FakeOptions{})
	w.connect(t, "zaki", fake)
	w.search("zaki", w.a, "inbox", "Banjarbaru", fake)
	w.imp("zaki", "inbox", ticket, "18c2ffffffff0000")
	w.drain(t, fake)
	// A failing token exchange logs its status only.
	if out := connectors.CompleteConnect(w.ctx, w.d, w.a, w.ids["zaki"], "c", "v", leakyTokenEndpoint, time.Now().UnixMilli()); out != "failed" {
		t.Fatal(out)
	}
	connectors.Disconnect(w.ctx, w.d, w.a, w.ids["zaki"], fake.Do)
	all := buf.String()
	if !strings.Contains(all, "[recall:jobs]") {
		t.Fatal("no job log")
	}
	for _, word := range append(secretWords, "fake-access-leak") {
		if strings.Contains(all, word) {
			t.Fatalf("log contains %q:\n%s", word, all)
		}
	}
}

func leakyTokenEndpoint(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 500, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":"server_error","access_token":"fake-access-leak"}`))}, nil
}
