package apps_test

// The WhatsApp connector: wa-hub's messages to one digest per chat per day.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zakiego/recall/apps/server/internal/apps"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/model"
)

const waKey = "wah_abcdefghijkl_SECRET"

// waMsg is one wa-hub feed item; sentAt is UTC.
func waMsg(account, waID, chatJid, chatName, sentAt, sender, text string, extra *jsonx.Object) apps.ChangeItem {
	kind := "dm"
	if strings.HasSuffix(chatJid, "@g.us") {
		kind = "group"
	}
	direction := "in"
	var from any = sender
	if sender == "" {
		direction, from = "out", nil
	}
	data := obj("account", account, "accountLabel", strings.ToUpper(account[:1])+account[1:], "chatId", 1.0, "chatJid", chatJid, "chatKind", kind, "chatName", chatName,
		"timezone", "Asia/Makassar", "sentAt", sentAt, "direction", direction, "sender", from, "type", "text", "text", text, "media", nil, "edited", false)
	return apps.ChangeItem{Kind: "message", ID: account + ":" + waID, UpdatedAt: sentAt, Data: merge(data, extra)}
}

func docBySource(e *env, ws, sid string) (model.Document, bool) {
	for _, d := range e.docs(ws) {
		if s(d.SourceID) == sid {
			return d, true
		}
	}
	return model.Document{}, false
}

func TestWhatsAppWritesOneDocPerChatPerDay(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "whatsapp", waKey, nil, "")
	const budi, family = "6281111@s.whatsapp.net", "1203630@g.us"
	feed := apps.NewFakeFeed(waKey,
		waMsg("personal", "A1", budi, "Budi", "2026-11-09T01:00:00Z", "Budi", "Jadi ketemu Jumat jam 3?", nil),
		waMsg("personal", "A2", budi, "Budi", "2026-11-09T01:05:00Z", "", "Oke, di kantor ya", nil),
		waMsg("personal", "G1", family, "Keluarga", "2026-11-09T12:00:00Z", "Ibu", "Jangan lupa\nbawa kue", nil),
		waMsg("personal", "G2", family, "Keluarga", "2026-11-09T12:01:00Z", "Ayah", "", obj("type", "image", "media", "image")),
		// Only a sticker: nothing worth a document.
		waMsg("vorneo", "S1", "6282222@s.whatsapp.net", "Pak Andi", "2026-11-09T03:00:00Z", "Pak Andi", "", obj("type", "sticker", "media", "sticker")),
		// 01:00 local on Nov 10: today, so it waits.
		waMsg("vorneo", "V1", "6282222@s.whatsapp.net", "Pak Andi", "2026-11-09T17:00:00Z", "Pak Andi", "Rumah masih ada?", nil),
	)
	r := e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T02:00:00Z"))
	if !r.OK || !r.CaughtUp || r.Documents != 2 || r.Staged != 6 {
		t.Fatal(r)
	}
	if feed.Calls[0].Limit != apps.WhatsAppPageLimit {
		t.Fatal("page limit", feed.Calls[0].Limit)
	}
	dm, ok := docBySource(e, e.a, "wa:personal:"+budi+":2026-11-09")
	if !ok {
		t.Fatal(e.docs(e.a))
	}
	if s(dm.Title) != "WhatsApp with Budi on Nov 9, 2026" || meta(dm, "source") != "whatsapp" || meta(dm, "chatKind") != "dm" ||
		meta(dm, "documentDate") != "2026-11-09" || meta(dm, "messages") != float64(2) {
		t.Fatalf("%s %+v", s(dm.Title), dm.Metadata)
	}
	want := "WhatsApp messages on the Personal (personal) account, in a chat with Budi, on Monday, Nov 9, 2026 (Asia/Makassar time). 2 messages. Lines from \"me\" were sent from this account.\n\n" +
		"09:00 Budi: Jadi ketemu Jumat jam 3?\n09:05 me: Oke, di kantor ya"
	if s(dm.Content) != want {
		t.Fatalf("%q", s(dm.Content))
	}
	group, ok := docBySource(e, e.a, "wa:personal:"+family+":2026-11-09")
	if !ok || s(group.Title) != "WhatsApp group Keluarga on Nov 9, 2026" ||
		!strings.Contains(s(group.Content), "20:00 Ibu: Jangan lupa bawa kue\n20:01 Ayah: [image]") {
		t.Fatalf("%q", s(group.Content))
	}
	// Today's message waits; the closed day's rows are gone.
	if st := e.staged(row.ID); len(st) != 1 || st[0].day != "2026-11-10" || st[0].key != "wa:vorneo:V1" {
		t.Fatal(st)
	}
	// Next day: it closes into its own document.
	if r := e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T17:00:00Z")); !r.OK || r.Documents != 1 {
		t.Fatal(r)
	}
	if _, ok := docBySource(e, e.a, "wa:vorneo:6282222@s.whatsapp.net:2026-11-10"); !ok {
		t.Fatal(e.docs(e.a))
	}
}

func TestWhatsAppNeverRewritesAChatDay(t *testing.T) {
	e := newEnv(t)
	row := e.setup(e.a, "whatsapp", waKey, nil, "")
	feed := apps.NewFakeFeed(waKey, waMsg("personal", "A1", "1@s.whatsapp.net", "Budi", "2026-11-09T01:00:00Z", "Budi", "Halo", nil))
	e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T02:00:00Z"))
	// A late message for a written day (a history sync) stays out.
	feed.Add(waMsg("personal", "A0", "1@s.whatsapp.net", "Budi", "2026-11-09T00:30:00Z", "", "Pagi", nil))
	late := e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T03:00:00Z"))
	if !late.OK || late.Staged != 0 || late.Documents != 0 || len(e.docs(e.a)) != 1 || len(e.staged(row.ID)) != 0 {
		t.Fatal(late)
	}
}

func TestWhatsAppFirstSyncReaches30Days(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "whatsapp", waKey, nil, "")
	feed := apps.NewFakeFeed(waKey,
		waMsg("personal", "OLD", "1@s.whatsapp.net", "Budi", "2026-08-01T03:00:00Z", "Budi", "Lama", nil),
		waMsg("personal", "NEW", "1@s.whatsapp.net", "Budi", "2026-11-01T03:00:00Z", "Budi", "Baru", nil))
	e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T02:00:00Z"))
	if d := e.docs(e.a); len(d) != 1 || s(d[0].SourceID) != "wa:personal:1@s.whatsapp.net:2026-11-01" {
		t.Fatal(d)
	}
}

func TestWhatsAppNoCloseWhileMoreToRead(t *testing.T) {
	e := newEnv(t)
	e.setup(e.a, "whatsapp", waKey, nil, "")
	var items []apps.ChangeItem
	for i := 0; i < 6; i++ {
		items = append(items, waMsg("personal", fmt.Sprintf("M%d", i), "1@s.whatsapp.net", "Budi", fmt.Sprintf("2026-11-09T0%d:00:00Z", i), "Budi", fmt.Sprintf("pesan %d", i), nil))
	}
	feed := apps.NewFakeFeed(waKey, items...)
	capped := e.sync(e.a, "whatsapp", apps.SyncDeps{Do: feed.Do, Now: at("2026-11-10T02:00:00Z"), MaxPages: 1, PageLimit: 2})
	if !capped.OK || capped.CaughtUp || capped.Documents != 0 {
		t.Fatal(capped)
	}
	done := e.sync(e.a, "whatsapp", deps(feed, "2026-11-10T03:00:00Z"))
	if !done.CaughtUp || done.Documents != 1 || meta(e.docs(e.a)[0], "messages") != float64(6) {
		t.Fatal(done)
	}
}

func TestWhatsAppDigestIsCapped(t *testing.T) {
	var msgs []apps.WAMessage
	for i := 0; i < 2000; i++ {
		msgs = append(msgs, apps.WAMessage{ID: fmt.Sprintf("personal:%05d", i), Account: "personal", ChatJid: "g@g.us", ChatKind: "group", ChatName: "Big",
			At: "2026-11-09T03:00:00Z", From: "Someone", Text: strings.Repeat("x", 40)})
	}
	days := apps.GroupWhatsAppDay(msgs)
	if len(days) != 1 {
		t.Fatal(len(days))
	}
	_, content := apps.WhatsAppDigest("2026-11-09", days[0], "Asia/Makassar")
	if len(content) > apps.WhatsAppMaxChars+100 || !strings.Contains(content, "more messages cut for length.)") {
		t.Fatal(len(content), content[len(content)-80:])
	}
}
