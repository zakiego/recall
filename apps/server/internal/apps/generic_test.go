package apps_test

import (
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
)

const notesKey = "notes-key-SECRET-789"

func doc(id, updatedAt, title, content string, deleted bool) apps.ChangeItem {
	return apps.ChangeItem{Kind: "document", ID: id, UpdatedAt: updatedAt, URL: "https://notes.example.com/n/" + id, Deleted: deleted,
		Data: obj("title", title, "content", content, "date", "2026-11-09")}
}

func event(id, group, title, at, from, text string) apps.ChangeItem {
	return apps.ChangeItem{Kind: "event", ID: id, UpdatedAt: at, Data: obj("group", group, "title", title, "at", at, "from", from, "text", text,
		"note", "Lines from \"me\" were sent by the owner.")}
}

func TestDocumentItems(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", notesKey, nil, "")
	feed := apps.NewFakeFeed(notesKey, doc("n1", "2026-11-09T01:00:00Z", "Trip plan", "Fly to Bali on Dec 3.", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 0 {
		t.Fatal(r)
	}
	d := e.docs(e.a)[0]
	if s(d.SourceID) != "feed:notes:n1" || s(d.Title) != "Trip plan" || s(d.Content) != "Fly to Bali on Dec 3.\n\nhttps://notes.example.com/n/n1" ||
		meta(d, "source") != "notes" || meta(d, "documentDate") != "2026-11-09" {
		t.Fatalf("%+v", d)
	}
	// The same item again costs nothing.
	feed.Add(doc("n1", "2026-11-09T02:00:00Z", "Trip plan", "Fly to Bali on Dec 3.", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T03:00:00Z")); !r.OK || r.Documents != 0 || r.Removed != 0 {
		t.Fatal(r)
	}
	// An edit replaces it.
	feed.Add(doc("n1", "2026-11-09T03:00:00Z", "Trip plan", "Fly to Bali on Dec 4.", false))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T04:00:00Z")); !r.OK || r.Documents != 1 || r.Removed != 1 {
		t.Fatal(r)
	}
	if ds := e.docs(e.a); len(ds) != 1 || !strings.HasPrefix(s(ds[0].Content), "Fly to Bali on Dec 4.") {
		t.Fatal(ds)
	}
	// A delete removes it.
	feed.Add(doc("n1", "2026-11-09T04:00:00Z", "", "", true))
	if r := e.sync(e.a, "notes", deps(feed, "2026-11-10T05:00:00Z")); !r.OK || r.Removed != 1 {
		t.Fatal(r)
	}
	if ds := e.docs(e.a); len(ds) != 0 {
		t.Fatal(ds)
	}
}

func TestEventItemsBecomeDayDigests(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "chat", notesKey, nil, "")
	feed := apps.NewFakeFeed(notesKey,
		// 09:00 and 09:05 local (Asia/Makassar) on Nov 9.
		event("m2", "rina", "Chat with Rina", "2026-11-09T01:05:00Z", "Rina", "See you   at 7"),
		event("m1", "rina", "Chat with Rina", "2026-11-09T01:00:00Z", "me", "Dinner tonight?"),
		event("g1", "family", "Family group", "2026-11-09T02:00:00Z", "", "Photos are up"),
		// 01:00 local on Nov 10: today, so it waits.
		event("m3", "rina", "Chat with Rina", "2026-11-09T17:00:00Z", "Rina", "Morning"),
		// Missing text: dropped.
		event("bad", "rina", "Chat with Rina", "2026-11-09T01:10:00Z", "Rina", ""))
	r := e.sync(e.a, "chat", deps(feed, "2026-11-10T02:00:00Z"))
	if !r.OK || !r.CaughtUp || r.Documents != 2 || r.Staged != 4 {
		t.Fatal(r)
	}
	ds := e.docs(e.a)
	if len(ds) != 2 || s(ds[0].SourceID) != "feed:chat:family:2026-11-09" || s(ds[1].SourceID) != "feed:chat:rina:2026-11-09" {
		t.Fatal(sourceIDs(ds))
	}
	rina := ds[1]
	want := "Chat with Rina on Monday, Nov 9, 2026 (Asia/Makassar time). 2 entries. Lines from \"me\" were sent by the owner.\n\n09:00 me: Dinner tonight?\n09:05 Rina: See you at 7"
	if s(rina.Title) != "Chat with Rina on Nov 9, 2026" || s(rina.Content) != want || meta(rina, "group") != "rina" || meta(rina, "entries") != float64(2) {
		t.Fatalf("%q %q", s(rina.Title), s(rina.Content))
	}
	if !strings.Contains(s(ds[0].Content), "\n10:00 Photos are up") {
		t.Fatal(s(ds[0].Content))
	}
	if st := e.staged(row.ID); len(st) != 1 || st[0].key != "ev:m3" || st[0].day != "2026-11-10" {
		t.Fatal(st)
	}
	// A late line for a day that is written doesn't reopen it.
	late := event("m4", "rina", "Chat with Rina", "2026-11-09T03:00:00Z", "Rina", "Late")
	late.UpdatedAt = "2026-11-10T02:30:00Z"
	feed.Add(late)
	if r := e.sync(e.a, "chat", deps(feed, "2026-11-10T03:00:00Z")); !r.OK || r.Documents != 0 || r.Staged != 0 {
		t.Fatal(r)
	}
	// The next day closes once it's over.
	if r := e.sync(e.a, "chat", deps(feed, "2026-11-11T02:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	if st := e.staged(row.ID); len(st) != 0 {
		t.Fatal(st)
	}
}

func TestUnknownKindsAreSkipped(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "notes", notesKey, nil, "")
	// The kinds the older built-in feeds sent are unknown now too.
	var items []apps.ChangeItem
	for _, k := range []string{"task", "transaction", "message", "recurring_rule"} {
		items = append(items, apps.ChangeItem{Kind: k, ID: k, UpdatedAt: "2026-11-09T01:00:00Z", Data: obj("title", "x")})
	}
	feed := apps.NewFakeFeed(notesKey, items...)
	r := e.sync(e.a, "notes", deps(feed, "2026-11-10T02:00:00Z"))
	if !r.OK || r.Documents != 0 || r.Staged != 0 || r.Skipped != 4 || len(e.docs(e.a)) != 0 {
		t.Fatal(r)
	}
}

// The ids the older built-in feeds used are plain feed ids now.
func TestAnyIdTakesGenericKinds(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "finansial", notesKey, nil, "")
	feed := apps.NewFakeFeed(notesKey, doc("d1", "2026-11-09T01:00:00Z", "Budget note", "Keep groceries under IDR 3,000,000.", false))
	if r := e.sync(e.a, "finansial", deps(feed, "2026-11-10T02:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	if ds := e.docs(e.a); len(ds) != 1 || s(ds[0].SourceID) != "feed:finansial:d1" {
		t.Fatal(sourceIDs(ds))
	}
	if v, _ := apps.ConnectorView(e.d, e.a, "finansial"); v.Name != "finansial" {
		t.Fatalf("%+v", v)
	}
}

func TestFeedIdsNamesListAndRemove(t *testing.T) {
	e := newEnv(t)
	key := notesKey
	bad := apps.SaveInput{WorkspaceID: e.a, Provider: "My Notes", Container: "life", BaseURL: "https://notes.example.com", Enabled: true}
	bad.APIKey.Set, bad.APIKey.Value = true, &key
	if _, err := apps.Save(e.d, bad); err == nil {
		t.Fatal("accepted a bad id")
	}
	e.setup(e.a, "notes", notesKey, obj("name", "My notes"), "")
	e.setup(e.a, "chat", notesKey, nil, "")
	e.setup(e.b, "other", notesKey, nil, "")
	list, err := apps.List(e.d, e.a)
	if err != nil || len(list) != 2 || list[0].Provider != "notes" || list[0].Name != "My notes" || list[1].Name != "chat" || !list[1].Configured {
		t.Fatalf("%+v %v", list, err)
	}
	if err := apps.Remove(e.d, e.a, "notes"); err != nil {
		t.Fatal(err)
	}
	if err := apps.Remove(e.d, e.a, "notes"); err == nil {
		t.Fatal("removed twice")
	}
	if list, _ := apps.List(e.d, e.a); len(list) != 1 || list[0].Provider != "chat" {
		t.Fatalf("%+v", list)
	}
	if list, _ := apps.List(e.d, e.b); len(list) != 1 {
		t.Fatalf("%+v", list)
	}
}

func TestParseDocumentLimits(t *testing.T) {
	if apps.ParseDocument(apps.ChangeItem{Kind: "document", ID: "x", Data: obj("title", "", "content", "x")}) != nil {
		t.Fatal("kept a document without a title")
	}
	d := apps.ParseDocument(apps.ChangeItem{Kind: "document", ID: "x", Data: obj("title", "T", "content", "C", "date", "Nov 9")})
	if d == nil || d.Date != "" || d.Fingerprint == "" {
		t.Fatalf("%+v", d)
	}
	if _, ok := apps.ParseEvent(apps.ChangeItem{Kind: "event", ID: "e", Data: jsonx.ObjectOf("group", "g", "title", "T", "at", "not a time", "text", "x")}); ok {
		t.Fatal("kept an event without a time")
	}
}
