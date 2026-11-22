package apps

// WhatsApp, read from wa-hub's changes feed: one digest document per chat
// per local day. Like Oslo's events, a day's messages wait in
// app_connector_staging until the day is over, then each chat that day
// becomes one document that never changes.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/dates"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/memories"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

const (
	// WhatsAppMaxChars caps one digest; a busy group's day past it is cut.
	WhatsAppMaxChars = 30_000
	// WhatsAppPageLimit and WhatsAppMaxPages: up to 10,000 messages a run.
	WhatsAppPageLimit = 200
	WhatsAppMaxPages  = 50
)

// WAMessage is one staged message.
type WAMessage struct {
	ID           string `json:"id"`
	Account      string `json:"account"`
	AccountLabel string `json:"accountLabel,omitempty"`
	ChatJid      string `json:"chatJid"`
	ChatKind     string `json:"chatKind"`
	ChatName     string `json:"chatName"`
	At           string `json:"at"`
	From         string `json:"from"`
	Text         string `json:"text,omitempty"`
	Media        string `json:"media,omitempty"`
}

// WhatsAppSourceID is the source_id of one chat's day.
func WhatsAppSourceID(account, chatJid, day string) string {
	return "wa:" + account + ":" + chatJid + ":" + day
}

func waChatKey(account, chatJid string) string { return account + ":" + chatJid }

// StageWhatsAppItems stages one page of wa-hub messages; returns how many
// were.
func StageWhatsAppItems(q db.Q, c Connector, items []ChangeItem, ctx StageContext) (int, error) {
	staged := 0
	written := map[string]bool{}
	for _, it := range items {
		if it.Kind != "message" {
			continue
		}
		key := "wa:" + it.ID
		if it.Deleted {
			if err := unstage(q, c, key, ""); err != nil {
				return staged, err
			}
			continue
		}
		d := it.Data
		m := WAMessage{
			ID: it.ID, Account: ostr(get(d, "account")), AccountLabel: ostr(get(d, "accountLabel")),
			ChatJid: ostr(get(d, "chatJid")), ChatKind: ostr(get(d, "chatKind")), ChatName: ostr(get(d, "chatName")),
			Text: ostr(get(d, "text")), Media: ostr(get(d, "media")),
		}
		sentAt, ok := oiso(get(d, "sentAt"))
		if !ok || m.Account == "" || m.ChatJid == "" || (m.Text == "" && m.Media == "") {
			continue
		}
		m.At = sentAt
		m.From = "me"
		if ostr(get(d, "direction")) != "out" {
			if m.From = ostr(get(d, "sender")); m.From == "" {
				m.From = "someone"
			}
		}
		if m.ChatName == "" {
			m.ChatName = m.ChatJid
		}
		day, ok := LocalDay(sentAt, ctx.Timezone)
		if !ok {
			continue
		}
		// The first sync only reaches back so far.
		if !ctx.CaughtUp && ctx.BackfillFrom != "" && day < ctx.BackfillFrom {
			continue
		}
		// That chat's day is written already; it doesn't change.
		sid := WhatsAppSourceID(m.Account, m.ChatJid, day)
		done, seen := written[sid]
		if !seen {
			_, found, err := memories.FindBySourceID(q, c.WorkspaceID, c.Container, sid)
			if err != nil {
				return staged, err
			}
			done, written[sid] = found, found
		}
		if done {
			continue
		}
		payload := jsonx.Stringify(m)
		if _, err := q.Exec(`INSERT INTO app_connector_staging (connector_id, day, item_key, payload) VALUES (?, ?, ?, ?)
			ON CONFLICT (connector_id, item_key) DO UPDATE SET payload = ? WHERE app_connector_staging.written_at IS NULL`, c.ID, day, key, payload, payload); err != nil {
			return staged, err
		}
		staged++
	}
	return staged, nil
}

// WhatsAppDayMessages is one day's staged messages.
func WhatsAppDayMessages(q db.Q, c Connector, day string) ([]WAMessage, error) {
	rows, err := q.Query(`SELECT payload FROM app_connector_staging WHERE connector_id = ? AND day = ? AND written_at IS NULL AND item_key LIKE 'wa:%'`, c.ID, day)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WAMessage
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		var m WAMessage
		if json.Unmarshal([]byte(p), &m) == nil {
			out = append(out, m)
		}
	}
	return out, rows.Err()
}

// WAChatDay is one chat's messages on one day, oldest first.
type WAChatDay struct {
	Account, AccountLabel, ChatJid, ChatKind, ChatName string
	Messages                                           []WAMessage
}

// GroupWhatsAppDay splits a day's messages by chat, in a stable order.
func GroupWhatsAppDay(msgs []WAMessage) []WAChatDay {
	byChat := map[string]*WAChatDay{}
	var keys []string
	for _, m := range msgs {
		k := waChatKey(m.Account, m.ChatJid)
		cd := byChat[k]
		if cd == nil {
			cd = &WAChatDay{Account: m.Account, ChatJid: m.ChatJid}
			byChat[k] = cd
			keys = append(keys, k)
		}
		cd.Messages = append(cd.Messages, m)
	}
	sort.Strings(keys)
	out := make([]WAChatDay, 0, len(keys))
	for _, k := range keys {
		cd := byChat[k]
		sort.SliceStable(cd.Messages, func(i, j int) bool {
			if c := strx.LocaleCompare(cd.Messages[i].At, cd.Messages[j].At); c != 0 {
				return c < 0
			}
			return cd.Messages[i].ID < cd.Messages[j].ID
		})
		// The newest message carries the current names.
		last := cd.Messages[len(cd.Messages)-1]
		cd.AccountLabel, cd.ChatKind, cd.ChatName = last.AccountLabel, last.ChatKind, last.ChatName
		out = append(out, *cd)
	}
	return out
}

// worthKeeping: a day of only stickers, photos and voice notes without a
// word says nothing a model can use.
func (cd WAChatDay) worthKeeping() bool {
	for _, m := range cd.Messages {
		if m.Text != "" {
			return true
		}
	}
	return false
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// WhatsAppDigest is one chat's day: its title and content.
func WhatsAppDigest(day string, cd WAChatDay, tz string) (string, string) {
	dateText := dates.Format(dates.Dated{Date: day, Precision: dates.Day})
	t, _ := time.Parse("2006-01-02", day)
	account := cd.Account
	if cd.AccountLabel != "" && cd.AccountLabel != cd.Account {
		account = cd.AccountLabel + " (" + cd.Account + ")"
	}
	where, title := "a chat with "+cd.ChatName, "WhatsApp with "+cd.ChatName+" on "+dateText
	if cd.ChatKind == "group" {
		where, title = "the group \""+cd.ChatName+"\"", "WhatsApp group "+cd.ChatName+" on "+dateText
	}
	head := fmt.Sprintf("WhatsApp messages on the %s account, in %s, on %s, %s (%s time). %s. Lines from \"me\" were sent from this account.",
		account, where, t.Weekday(), dateText, tz, plural(len(cd.Messages), "message", "messages"))
	var b strings.Builder
	b.WriteString(head)
	b.WriteString("\n")
	size := strx.Len16(head) + 1
	for i, m := range cd.Messages {
		line := "\n" + localTime(m.At, tz) + " " + m.From + ": "
		if m.Media != "" {
			line += "[" + m.Media + "] "
		}
		line += oneLine(m.Text)
		line = strings.TrimRight(line, " ")
		n := strx.Len16(line)
		if size+n > WhatsAppMaxChars {
			b.WriteString(fmt.Sprintf("\n(%s cut for length.)", plural(len(cd.Messages)-i, "more message", "more messages")))
			break
		}
		b.WriteString(line)
		size += n
	}
	return title, b.String()
}

// closeWhatsAppDays writes one document per chat for every day that is over.
func closeWhatsAppDays(d *db.DB, c Connector, tz string, now int64) (int, error) {
	days, err := ClosedDays(d, c, dates.TodayIn(tz, now))
	if err != nil {
		return 0, err
	}
	written := 0
	for _, day := range days {
		err := d.Tx(func(tx db.Q) error {
			msgs, err := WhatsAppDayMessages(tx, c, day)
			if err != nil {
				return err
			}
			for _, cd := range GroupWhatsAppDay(msgs) {
				if !cd.worthKeeping() {
					continue
				}
				title, content := WhatsAppDigest(day, cd, tz)
				meta := jsonx.ObjectOf("source", "whatsapp", "kind", "chat_day", "account", cd.Account, "chat", cd.ChatJid, "chatKind", cd.ChatKind,
					"chatName", cd.ChatName, "day", day, "messages", len(cd.Messages))
				ok, err := writeDoc(tx, c, docInput{WhatsAppSourceID(cd.Account, cd.ChatJid, day), title, content, day, meta}, now)
				if err != nil {
					return err
				}
				if ok {
					written++
				}
			}
			_, err = tx.Exec(`DELETE FROM app_connector_staging WHERE connector_id = ? AND day = ?`, c.ID, day)
			return err
		})
		if err != nil {
			return written, err
		}
	}
	return written, nil
}
