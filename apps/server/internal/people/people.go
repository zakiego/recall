package people

import (
	"context"
	"database/sql"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/zakiego/recall/apps/server/internal/config"
	"github.com/zakiego/recall/apps/server/internal/db"
	"github.com/zakiego/recall/apps/server/internal/embed"
	"github.com/zakiego/recall/apps/server/internal/gateway"
	"github.com/zakiego/recall/apps/server/internal/ids"
	"github.com/zakiego/recall/apps/server/internal/jsonx"
	"github.com/zakiego/recall/apps/server/internal/model"
	"github.com/zakiego/recall/apps/server/internal/search"
	"github.com/zakiego/recall/apps/server/internal/sensitive"
	"github.com/zakiego/recall/apps/server/internal/strx"
)

// Embed embeds descriptors; tests replace it.
var Embed = func(texts []string) ([][]float32, error) { return embed.Embed(texts, embed.Passage) }

func nowMs() int64 { return time.Now().UnixMilli() }

// SubjectNames is subjectNames: in the default container the subject is the
// workspace's owner(s), so their display name and username count as the
// subject there.
func SubjectNames(q db.Q, scope search.Scope) ([]string, error) {
	if scope.Container != config.Load().DefaultContainer {
		return []string{}, nil
	}
	rows, err := q.Query(`SELECT u.name, u.username, u.email FROM member m JOIN user u ON u.id = m.user_id
       WHERE m.organization_id = ? AND m.role = 'owner'`, scope.WorkspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name, email string
		var username sql.NullString
		if err := rows.Scan(&name, &username, &email); err != nil {
			return nil, err
		}
		for _, s := range []string{name, username.String} {
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out, rows.Err()
}

// --- reads ------------------------------------------------------------------------

// Row is PersonRow: the people row plus when they were last mentioned, how
// many current facts mention them and the newest of those.
type Row struct {
	ID              string   `json:"id"`
	WorkspaceID     string   `json:"workspaceId"`
	Container       string   `json:"container"`
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Role            *string  `json:"role"`
	Organization    *string  `json:"organization"`
	Emails          []string `json:"emails"`
	Handles         []string `json:"handles"`
	Summary         string   `json:"summary"`
	Manual          bool     `json:"manual"`
	CreatedAt       int64    `json:"createdAt"`
	UpdatedAt       int64    `json:"updatedAt"`
	LastMentionedAt *int64   `json:"lastMentionedAt"`
	FactCount       int64    `json:"factCount"`
	LatestFact      *string  `json:"latestFact"`
}

const stats = `(SELECT max(m.created_at) FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id) AS lastMentionedAt,
  (SELECT count(*) FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id AND m.valid_to IS NULL) AS factCount,
  (SELECT m.fact FROM person_memories pm JOIN memories m ON m.id = pm.memory_id WHERE pm.person_id = p.id AND m.valid_to IS NULL
    ORDER BY m.created_at DESC, m.id DESC LIMIT 1) AS latestFact`

const selectPerson = `SELECT p.id, p.workspace_id AS workspaceId, p.container, p.name, p.aliases, p.role, p.organization, p.emails, p.handles,
  p.summary, p.manual, p.created_at AS createdAt, p.updated_at AS updatedAt, ` + stats + ` FROM people p`

// parseList is parseList: a JSON array's strings, else none.
func parseList(s sql.NullString) []string {
	text := "[]"
	if s.Valid {
		text = s.String
	}
	v, err := jsonx.Parse([]byte(text))
	out := []string{}
	if err != nil {
		return out
	}
	if arr, ok := v.([]any); ok {
		for _, x := range arr {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
	}
	return out
}

func scanRows(rows *sql.Rows) ([]Row, error) {
	defer rows.Close()
	out := []Row{}
	for rows.Next() {
		var r Row
		var aliases, emails, handles, role, org, latest sql.NullString
		var manual sql.NullInt64
		var last sql.NullInt64
		if err := rows.Scan(&r.ID, &r.WorkspaceID, &r.Container, &r.Name, &aliases, &role, &org, &emails, &handles, &r.Summary, &manual, &r.CreatedAt, &r.UpdatedAt, &last, &r.FactCount, &latest); err != nil {
			return nil, err
		}
		r.Aliases, r.Emails, r.Handles = parseList(aliases), parseList(emails), parseList(handles)
		r.Role, r.Organization, r.LatestFact = model.Str(role), model.Str(org), model.Str(latest)
		r.Manual = manual.Valid && manual.Int64 != 0
		r.LastMentionedAt = model.Int(last)
		out = append(out, r)
	}
	return out, rows.Err()
}

func inScope(q db.Q, scope search.Scope) ([]Row, error) {
	rows, err := q.Query(selectPerson+` WHERE p.workspace_id = ? AND p.container = ?`, scope.WorkspaceID, scope.Container)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// GetRow is getPersonRow; ok false when there is none in the workspace.
func GetRow(q db.Q, workspaceID, id string) (Row, bool, error) {
	rows, err := q.Query(selectPerson+` WHERE p.id = ? AND p.workspace_id = ?`, id, workspaceID)
	if err != nil {
		return Row{}, false, err
	}
	list, err := scanRows(rows)
	if err != nil || len(list) == 0 {
		return Row{}, false, err
	}
	return list[0], true, nil
}

// ListOptions are listPeople's options; Sort "name" or "recent".
type ListOptions struct {
	WorkspaceID, Container string
	Q                      string
	Sort                   string
	Limit, Offset          *int
}

// ListResult is listPeople's result.
type ListResult struct {
	People     []Row `json:"people"`
	Total      int   `json:"total"`
	NextOffset *int  `json:"nextOffset"`
}

// List is listPeople. The filter runs on NameKey, so "elodie" finds
// "Élodie" the way the resolver does.
func List(q db.Q, o ListOptions) (ListResult, error) {
	limit := 50
	if o.Limit != nil {
		limit = *o.Limit
	}
	limit = min(max(limit, 1), 200)
	offset := 0
	if o.Offset != nil {
		offset = max(*o.Offset, 0)
	}
	key := NameKey(o.Q)
	order := "lastMentionedAt IS NULL, lastMentionedAt DESC, lower(p.name) ASC, p.id ASC"
	if o.Sort == "name" {
		order = "lower(p.name) ASC, p.id ASC"
	}
	rows, err := q.Query(selectPerson+` WHERE p.workspace_id = ? AND p.container = ? ORDER BY `+order, o.WorkspaceID, o.Container)
	if err != nil {
		return ListResult{}, err
	}
	all, err := scanRows(rows)
	if err != nil {
		return ListResult{}, err
	}
	found := all
	if key != "" {
		found = []Row{}
		for _, p := range all {
			fields := append(append([]string{p.Name}, p.Aliases...), deref(p.Role), deref(p.Organization))
			fields = append(append(fields, p.Emails...), p.Handles...)
			for _, v := range fields {
				if strings.Contains(NameKey(v), key) {
					found = append(found, p)
					break
				}
			}
		}
	}
	end := min(offset+limit, len(found))
	page := []Row{}
	if offset < len(found) {
		page = found[offset:end]
	}
	res := ListResult{People: page, Total: len(found)}
	if offset+len(page) < len(found) {
		n := offset + len(page)
		res.NextOffset = &n
	}
	return res, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Source is a fact's source document.
type Source struct {
	ID    string  `json:"id"`
	Title *string `json:"title"`
	Type  string  `json:"type"`
	URL   *string `json:"url"`
}

// Fact is PersonFact.
type Fact struct {
	ID             string  `json:"id"`
	Fact           string  `json:"fact"`
	Kind           string  `json:"kind"`
	CreatedAt      int64   `json:"createdAt"`
	ValidTo        *int64  `json:"validTo"`
	SupersedesID   *string `json:"supersedesId"`
	ReviewNote     *string `json:"reviewNote"`
	EventDate      *string `json:"eventDate"`
	EventPrecision *string `json:"eventPrecision"`
	DueDate        *string `json:"dueDate"`
	DuePrecision   *string `json:"duePrecision"`
	Source         *Source `json:"source"`
}

// Facts is personFacts: the facts that mention a person, newest first;
// current ones only unless history. limit 0 is 500.
func Facts(q db.Q, workspaceID, personID string, history bool, limit int) ([]Fact, error) {
	where := ""
	if !history {
		where = " AND m.valid_to IS NULL"
	}
	rows, err := q.Query(`SELECT m.id, m.fact, m.kind, m.created_at, m.valid_to, m.supersedes_id, m.review_note, m.event_date, m.event_precision, m.due_date, m.due_precision,
		d.id, d.title, d.source_type, d.url
		FROM person_memories pm
		JOIN memories m ON m.id = pm.memory_id AND m.workspace_id = ?
		LEFT JOIN documents d ON d.id = m.source_document_id AND d.workspace_id = ?
		WHERE pm.person_id = ? AND pm.workspace_id = ?`+where, workspaceID, workspaceID, personID, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Fact{}
	for rows.Next() {
		var f Fact
		var validTo sql.NullInt64
		var sup, note, ed, ep, dd, dp, did, dtitle, dtype, durl sql.NullString
		if err := rows.Scan(&f.ID, &f.Fact, &f.Kind, &f.CreatedAt, &validTo, &sup, &note, &ed, &ep, &dd, &dp, &did, &dtitle, &dtype, &durl); err != nil {
			return nil, err
		}
		f.ValidTo, f.SupersedesID, f.ReviewNote = model.Int(validTo), model.Str(sup), model.Str(note)
		f.EventDate, f.EventPrecision, f.DueDate, f.DuePrecision = model.Str(ed), model.Str(ep), model.Str(dd), model.Str(dp)
		if did.Valid && did.String != "" {
			typ := "text"
			if dtype.Valid {
				typ = dtype.String
			}
			f.Source = &Source{ID: did.String, Title: model.Str(dtitle), Type: typ, URL: model.Str(durl)}
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	if limit <= 0 {
		limit = 500
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Person is getPerson's result.
type Person struct {
	Person Row    `json:"person"`
	Facts  []Fact `json:"facts"`
}

// Get is getPerson; ok false when there is no such person.
func Get(q db.Q, workspaceID, id string, history bool) (Person, bool, error) {
	p, ok, err := GetRow(q, workspaceID, id)
	if err != nil || !ok {
		return Person{}, false, err
	}
	facts, err := Facts(q, workspaceID, id, history, 0)
	if err != nil {
		return Person{}, false, err
	}
	return Person{Person: p, Facts: facts}, true, nil
}

// Ref is PersonRef.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ForMemories is peopleForMemories: the people each memory mentions, keyed
// by memory id in the order the rows come.
func ForMemories(q db.Q, workspaceID string, memoryIDs []string) (*jsonx.Map[[]Ref], error) {
	out := jsonx.NewMap[[]Ref]()
	if len(memoryIDs) == 0 {
		return out, nil
	}
	args := []any{workspaceID, workspaceID}
	for _, id := range memoryIDs {
		args = append(args, id)
	}
	rows, err := q.Query(`SELECT pm.memory_id, p.id, p.name FROM person_memories pm
		JOIN people p ON p.id = pm.person_id AND p.workspace_id = ?
		WHERE pm.workspace_id = ? AND pm.memory_id IN (`+placeholders(len(memoryIDs))+`) ORDER BY p.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid string
		var r Ref
		if err := rows.Scan(&mid, &r.ID, &r.Name); err != nil {
			return nil, err
		}
		list, _ := out.Get(mid)
		out.Set(mid, append(list, r))
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// --- resolve ------------------------------------------------------------------------

type candidate struct {
	person Row
	exact  bool
}

func candidates(all []Row, m gateway.PersonMention) []candidate {
	var order []string
	byID := map[string]candidate{}
	add := func(p Row, exact bool) {
		cur, ok := byID[p.ID]
		if !ok {
			order = append(order, p.ID)
		}
		if !ok || (exact && !cur.exact) {
			byID[p.ID] = candidate{p, exact}
		}
	}
	email := ""
	if mine, ok := personalEmail(m.Email); ok {
		email = emailKey(mine)
	}
	handle := ""
	if m.Handle != nil && *m.Handle != "" {
		handle = handleKey(*m.Handle)
	}
	key := NameKey(m.Name)
	var aliasKeys []string
	for _, a := range m.Aliases {
		if k := NameKey(a); k != "" {
			aliasKeys = append(aliasKeys, k)
		}
	}
	for _, p := range all {
		names := append([]string{p.Name}, p.Aliases...)
		keys := make([]string, len(names))
		for i, n := range names {
			keys[i] = NameKey(n)
		}
		// A shortened form of the full name ("Clara" for "Clara Simmons") is
		// shared by many people, so matching on it alone needs the embedding.
		var firm []string
		for _, n := range names {
			if n == p.Name || !isPartialOf(n, p.Name) {
				firm = append(firm, NameKey(n))
			}
		}
		// A new email rules out a first-name match; a full name that matches
		// still goes to the embedding check.
		var theirs []string
		for _, e := range p.Emails {
			if !IsSharedMailbox(e) {
				theirs = append(theirs, emailKey(e))
			}
		}
		otherEmail := email != "" && len(theirs) > 0 && !contains(theirs, email)
		hasHandle := false
		if handle != "" {
			for _, h := range p.Handles {
				if handleKey(h) == handle {
					hasHandle = true
				}
			}
		}
		strong := false
		lookAlike := false
		for _, n := range names {
			mm := MatchNames(m.Name, n)
			strong = strong || mm == Strong
			lookAlike = lookAlike || mm != ""
		}
		aliasHit := false
		for _, a := range aliasKeys {
			if contains(keys, a) {
				aliasHit = true
			}
		}
		switch {
		case email != "" && contains(theirs, email):
			add(p, true)
		case hasHandle:
			add(p, true)
		case contains(firm, key):
			add(p, true)
		case otherEmail && !strong:
			continue
		case contains(keys, key) || aliasHit || lookAlike:
			add(p, false)
		}
	}
	out := make([]candidate, len(order))
	for i, id := range order {
		out[i] = byID[id]
	}
	return out
}

func cosine(a, b []float32) float64 {
	s := 0.0
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// describe is "name. role org. summary. facts" with empty parts left out.
func describe(name string, role, org, summary *string, facts []string) string {
	ro := []string{}
	for _, s := range []*string{role, org} {
		if s != nil && *s != "" {
			ro = append(ro, *s)
		}
	}
	parts := []string{name, strings.Join(ro, " "), deref(summary)}
	if len(facts) > descriptorFacts {
		facts = facts[:descriptorFacts]
	}
	parts = append(parts, facts...)
	var keep []string
	for _, s := range parts {
		if s != "" && strx.Trim(s) != "" {
			keep = append(keep, s)
		}
	}
	return strings.Join(keep, ". ")
}

func factTexts(q db.Q, workspaceID, personID string) ([]string, error) {
	fs, err := Facts(q, workspaceID, personID, false, descriptorFacts)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Fact
	}
	return out, nil
}

// Match is a resolved person; Similarity nil for a single exact match.
type Match struct {
	Person     Row
	Similarity *float64
}

// MatchPerson is matchPerson: an exact match (email, handle, name or alias,
// ignoring case and accents) wins; several are told apart by the embedding;
// a fuzzy match needs the embedding to agree. nil is a new person. all nil
// reads the scope's people.
func MatchPerson(q db.Q, scope search.Scope, m gateway.PersonMention, facts []string, all []Row) (*Match, error) {
	if all == nil {
		var err error
		if all, err = inScope(q, scope); err != nil {
			return nil, err
		}
	}
	found := candidates(all, m)
	if len(found) == 0 {
		return nil, nil
	}
	var exact []candidate
	for _, c := range found {
		if c.exact {
			exact = append(exact, c)
		}
	}
	if len(exact) == 1 {
		return &Match{Person: exact[0].person}, nil
	}
	pool := found
	if len(exact) > 0 {
		pool = exact
	}
	texts := []string{describe(m.Name, m.Role, m.Organization, m.Summary, facts)}
	for _, c := range pool {
		ft, err := factTexts(q, scope.WorkspaceID, c.person.ID)
		if err != nil {
			return nil, err
		}
		texts = append(texts, describe(c.person.Name, c.person.Role, c.person.Organization, &c.person.Summary, ft))
	}
	vectors, err := Embed(texts)
	if err != nil {
		return nil, err
	}
	scored := make([]Match, len(pool))
	for i, c := range pool {
		s := cosine(vectors[0], vectors[i+1])
		scored[i] = Match{Person: c.person, Similarity: &s}
	}
	sort.SliceStable(scored, func(i, j int) bool { return *scored[i].Similarity > *scored[j].Similarity })
	best := scored[0]
	if len(exact) > 0 || *best.Similarity >= FuzzyPersonSimilarity {
		return &best, nil
	}
	return nil, nil
}

// uniq trims, drops empties and repeats by key, and keeps max.
func uniq(list []string, key func(string) string, max int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range list {
		s := strx.Trim(x)
		if s == "" {
			continue
		}
		k := key(s)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, s)
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

func without(list []string, name string) []string {
	out := []string{}
	nk := NameKey(name)
	for _, a := range list {
		if NameKey(a) != nk {
			out = append(out, a)
		}
	}
	return out
}

// clean is clean: nil for undefined, empty or sensitive text.
func clean(s *string) *string {
	if s == nil || *s == "" || len(sensitive.Kinds(*s)) > 0 {
		return nil
	}
	t := strx.Trim(*s)
	return &t
}

type fields struct {
	name               string
	aliases            []string
	role, organization *string
	emails, handles    []string
	summary            string
}

// merged folds a mention into an existing person, or gives the row for a
// new one. A hand-edited person keeps its name, role, organization and
// summary.
func merged(existing *Row, m gateway.PersonMention) fields {
	role, org, summary := clean(m.Role), clean(m.Organization), clean(m.Summary)
	email, hasEmail := personalEmail(m.Email)
	if existing == nil {
		f := fields{name: strx.Trim(m.Name), aliases: without(uniq(m.Aliases, NameKey, maxAliases), m.Name), role: role, organization: org, emails: []string{}, handles: []string{}, summary: deref(summary)}
		if hasEmail {
			f.emails = []string{email}
		}
		if m.Handle != nil && *m.Handle != "" {
			f.handles = []string{strx.Trim(*m.Handle)}
		}
		return f
	}
	// A fuller name than the one on file ("Clara Simmons" for "Clara") becomes the name.
	fuller := !existing.Manual && len(tokens(m.Name)) > len(tokens(existing.Name)) && NamesLookAlike(m.Name, existing.Name)
	name := existing.Name
	if fuller {
		name = strx.Trim(m.Name)
	}
	f := fields{name: name}
	f.aliases = without(uniq(append(append(append([]string{}, existing.Aliases...), existing.Name, m.Name), m.Aliases...), NameKey, maxAliases), name)
	if existing.Manual {
		f.role, f.organization, f.summary = existing.Role, existing.Organization, existing.Summary
	} else {
		f.role, f.organization = existing.Role, existing.Organization
		if role != nil {
			f.role = role
		}
		if org != nil {
			f.organization = org
		}
		f.summary = existing.Summary
		if summary != nil && *summary != "" {
			f.summary = *summary
		}
	}
	emails := append([]string{}, existing.Emails...)
	if hasEmail {
		emails = append(emails, email)
	}
	f.emails = uniq(emails, emailKey, maxContacts)
	handles := append([]string{}, existing.Handles...)
	if m.Handle != nil && *m.Handle != "" {
		handles = append(handles, *m.Handle)
	}
	f.handles = uniq(handles, handleKey, maxContacts)
	return f
}

// LinkStats are linkMentions' counts.
type LinkStats struct {
	Linked  int `json:"linked"`
	Created int `json:"created"`
	Skipped int `json:"skipped"`
}

// Mention is a mention and the memories it names.
type Mention struct {
	Mention   gateway.PersonMention
	MemoryIDs []string
}

func listJSON(l []string) string { return jsonx.Stringify(l) }

// LinkMentions is linkMentions: each mention resolves to a person (created
// when nothing matches) and is linked to its memories. Mentions of the
// container's subject, of teams, and memories outside the scope are skipped.
func LinkMentions(d *db.DB, scope search.Scope, mentions []Mention) (LinkStats, error) {
	var st LinkStats
	var allIDs []string
	seen := map[string]bool{}
	for _, x := range mentions {
		for _, id := range x.MemoryIDs {
			if !seen[id] {
				seen[id] = true
				allIDs = append(allIDs, id)
			}
		}
	}
	if len(allIDs) == 0 {
		return st, nil
	}
	owners, err := SubjectNames(d, scope)
	if err != nil {
		return st, err
	}
	facts := map[string]string{}
	args := []any{scope.WorkspaceID, scope.Container}
	for _, id := range allIDs {
		args = append(args, id)
	}
	rows, err := d.Query(`SELECT id, fact FROM memories WHERE workspace_id = ? AND container = ? AND id IN (`+placeholders(len(allIDs))+`)`, args...)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var id, fact string
		if err := rows.Scan(&id, &fact); err != nil {
			rows.Close()
			return st, err
		}
		facts[id] = fact
	}
	rows.Close()
	for _, x := range mentions {
		var memIDs []string
		once := map[string]bool{}
		for _, id := range x.MemoryIDs {
			if !once[id] {
				once[id] = true
				if _, ok := facts[id]; ok {
					memIDs = append(memIDs, id)
				}
			}
		}
		m := x.Mention
		if len(memIDs) == 0 || IsContainerOwner(scope.Container, m, owners) || LooksLikeOrganization(m.Name, m.Email) || len(sensitive.Kinds(m.Name)) > 0 {
			st.Skipped++
			continue
		}
		texts := make([]string, len(memIDs))
		for i, id := range memIDs {
			texts[i] = facts[id]
		}
		match, err := MatchPerson(d, scope, m, texts, nil)
		if err != nil {
			return st, err
		}
		var existing *Row
		if match != nil {
			existing = &match.Person
		}
		next := merged(existing, m)
		err = d.Tx(func(tx db.Q) error {
			personID := ""
			if existing != nil {
				personID = existing.ID
				if _, err := tx.Exec(`UPDATE people SET name = ?, aliases = ?, role = ?, organization = ?, emails = ?, handles = ?, summary = ?, updated_at = ? WHERE id = ? AND workspace_id = ?`,
					next.name, listJSON(next.aliases), next.role, next.organization, listJSON(next.emails), listJSON(next.handles), next.summary, nowMs(), personID, scope.WorkspaceID); err != nil {
					return err
				}
			} else {
				personID = ids.New(ids.Person)
				if _, err := tx.Exec(`INSERT INTO people (id, workspace_id, container, name, aliases, role, organization, emails, handles, summary) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					personID, scope.WorkspaceID, scope.Container, next.name, listJSON(next.aliases), next.role, next.organization, listJSON(next.emails), listJSON(next.handles), next.summary); err != nil {
					return err
				}
				st.Created++
			}
			for _, memoryID := range memIDs {
				n, err := db.Changes(tx.Exec(`INSERT INTO person_memories (person_id, memory_id, workspace_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, personID, memoryID, scope.WorkspaceID))
				if err != nil {
					return err
				}
				st.Linked += int(n)
			}
			return nil
		})
		if err != nil {
			return st, err
		}
	}
	return st, nil
}

// MarkScanned is markScanned: facts already checked for people.
func MarkScanned(q db.Q, workspaceID string, memoryIDs []string) error {
	for _, id := range memoryIDs {
		if _, err := q.Exec(`INSERT INTO people_scans (memory_id, workspace_id) VALUES (?, ?) ON CONFLICT DO NOTHING`, id, workspaceID); err != nil {
			return err
		}
	}
	return nil
}

// Written is one row an extract wrote, under its ref ("a1", "u2", "c1").
type Written struct {
	Ref string
	Row model.Memory
}

// LinkExtracted is linkExtractedPeople: the extract named facts by ref; the
// written rows are marked checked only after their links are written.
func LinkExtracted(d *db.DB, scope search.Scope, mentions []gateway.PersonMention, written []Written) (LinkStats, error) {
	byRef := map[string]string{}
	for _, w := range written {
		byRef[w.Ref] = w.Row.ID
	}
	list := make([]Mention, len(mentions))
	for i, m := range mentions {
		var memIDs []string
		for _, f := range m.Facts {
			if id, ok := byRef[strx.Lower(strx.Trim(f))]; ok {
				memIDs = append(memIDs, id)
			}
		}
		list[i] = Mention{Mention: m, MemoryIDs: memIDs}
	}
	st, err := LinkMentions(d, scope, list)
	if err != nil {
		return st, err
	}
	all := make([]string, len(written))
	for i, w := range written {
		all[i] = w.Row.ID
	}
	return st, MarkScanned(d, scope.WorkspaceID, all)
}

// --- backfill ---------------------------------------------------------------------

// BackfillBatch is how many facts one backfill call sends.
const BackfillBatch = 40

// BackfillStats are backfillPeople's counts.
type BackfillStats struct {
	Scanned int `json:"scanned"`
	Linked  int `json:"linked"`
	Created int `json:"created"`
	Calls   int `json:"calls"`
}

// Backfill is backfillPeople: links current facts never checked for people,
// a batch per call; each batch is marked scanned once its links are
// written. maxBatches <= 0 is no limit.
func Backfill(ctx context.Context, d *db.DB, scope search.Scope, do gateway.Do, maxBatches int) (BackfillStats, error) {
	var st BackfillStats
	for batch := 0; maxBatches <= 0 || batch < maxBatches; batch++ {
		rows, err := d.Query(`SELECT m.id, m.fact FROM memories m
         WHERE m.workspace_id = ? AND m.container = ? AND m.valid_to IS NULL
           AND NOT EXISTS (SELECT 1 FROM people_scans s WHERE s.memory_id = m.id)
         ORDER BY m.created_at, m.id LIMIT ?`, scope.WorkspaceID, scope.Container, BackfillBatch)
		if err != nil {
			return st, err
		}
		type fact struct{ id, fact string }
		var list []fact
		for rows.Next() {
			var f fact
			if err := rows.Scan(&f.id, &f.fact); err != nil {
				rows.Close()
				return st, err
			}
			list = append(list, f)
		}
		rows.Close()
		if len(list) == 0 {
			break
		}
		refs := make([]gateway.RefFact, len(list))
		for i, f := range list {
			refs[i] = gateway.RefFact{Ref: i + 1, Fact: f.fact}
		}
		result, _, err := gateway.ChatJSON(ctx, gateway.Request{System: gateway.PeopleBackfillSystem(scope.Container), Prompt: gateway.PeopleBackfillPrompt(refs)}, gateway.ParsePeopleBackfill, do)
		if err != nil {
			return st, err
		}
		st.Calls++
		mentions := make([]Mention, len(result))
		for i, m := range result {
			var memIDs []string
			for _, f := range m.Facts {
				// rows[Number(f) - 1]?.id
				n := strx.Number(f) - 1
				if n >= 0 && n == math.Trunc(n) && int(n) < len(list) {
					memIDs = append(memIDs, list[int(n)].id)
				}
			}
			mentions[i] = Mention{Mention: m, MemoryIDs: memIDs}
		}
		s, err := LinkMentions(d, scope, mentions)
		if err != nil {
			return st, err
		}
		scanned := make([]string, len(list))
		for i, f := range list {
			scanned[i] = f.id
		}
		if err := MarkScanned(d, scope.WorkspaceID, scanned); err != nil {
			return st, err
		}
		st.Scanned += len(list)
		st.Linked += s.Linked
		st.Created += s.Created
	}
	return st, nil
}

// Pending is a container with facts nobody has checked for people.
type Pending struct {
	Container string `json:"container"`
	Pending   int64  `json:"pending"`
}

// ContainersToBackfill is containersToBackfill.
func ContainersToBackfill(q db.Q, workspaceID string) ([]Pending, error) {
	rows, err := q.Query(`SELECT m.container, count(*) AS pending FROM memories m
       WHERE m.workspace_id = ? AND m.valid_to IS NULL AND NOT EXISTS (SELECT 1 FROM people_scans s WHERE s.memory_id = m.id)
       GROUP BY m.container ORDER BY m.container`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pending{}
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.Container, &p.Pending); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- edits ------------------------------------------------------------------------

// Error is PersonError: a message for the person editing.
type Error struct{ Msg string }

func (e *Error) Error() string  { return e.Msg }
func (e *Error) JSName() string { return "Error" }

// OptString is a patch field that may be undefined, null or a string.
type OptString struct {
	Set   bool
	Value *string
}

// Patch is PersonPatch; nil slices and pointers are undefined.
type Patch struct {
	Name         *string
	Aliases      *[]string
	Role         OptString
	Organization OptString
	Summary      *string
	Emails       *[]string
	Handles      *[]string
}

// trimOrNil is s?.trim() || null.
func trimOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strx.Trim(*s)
	if t == "" {
		return nil
	}
	return &t
}

// Update is updatePerson; ok false when there is no such person.
func Update(q db.Q, workspaceID, id string, p Patch) (Row, bool, error) {
	cur, ok, err := GetRow(q, workspaceID, id)
	if err != nil || !ok {
		return Row{}, false, err
	}
	name := cur.Name
	if p.Name != nil && strx.Trim(*p.Name) != "" {
		name = strx.Trim(*p.Name)
	}
	aliases, emails, handles := cur.Aliases, cur.Emails, cur.Handles
	if p.Aliases != nil {
		aliases = *p.Aliases
	}
	if p.Emails != nil {
		emails = *p.Emails
	}
	if p.Handles != nil {
		handles = *p.Handles
	}
	role, org, summary := cur.Role, cur.Organization, cur.Summary
	if p.Role.Set {
		role = trimOrNil(p.Role.Value)
	}
	if p.Organization.Set {
		org = trimOrNil(p.Organization.Value)
	}
	if p.Summary != nil {
		summary = strx.Trim(*p.Summary)
	}
	if _, err := q.Exec(`UPDATE people SET name = ?, aliases = ?, role = ?, organization = ?, summary = ?, emails = ?, handles = ?, manual = 1, updated_at = ? WHERE id = ? AND workspace_id = ?`,
		name, listJSON(without(uniq(aliases, NameKey, maxAliases), name)), role, org, summary, listJSON(uniq(emails, emailKey, maxContacts)), listJSON(uniq(handles, handleKey, maxContacts)), nowMs(), id, workspaceID); err != nil {
		return Row{}, false, err
	}
	r, _, err := GetRow(q, workspaceID, id)
	return r, true, err
}

// Merge is mergePeople: everything from dropID moves into keepID, then
// dropID is deleted. Both must be in the same workspace and container.
func Merge(q db.Q, workspaceID, keepID, dropID string) (Row, error) {
	if keepID == dropID {
		return Row{}, &Error{"Pick two different people to merge."}
	}
	keep, ok1, err := GetRow(q, workspaceID, keepID)
	if err != nil {
		return Row{}, err
	}
	drop, ok2, err := GetRow(q, workspaceID, dropID)
	if err != nil {
		return Row{}, err
	}
	if !ok1 || !ok2 {
		return Row{}, &Error{"No such person."}
	}
	if keep.Container != drop.Container {
		return Row{}, &Error{"Only people in the same container can be merged."}
	}
	err = q.Tx(func(tx db.Q) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO person_memories (person_id, memory_id, workspace_id, created_at)
         SELECT ?, memory_id, workspace_id, created_at FROM person_memories WHERE person_id = ? AND workspace_id = ?`, keepID, dropID, workspaceID); err != nil {
			return err
		}
		role, org := keep.Role, keep.Organization
		if role == nil {
			role = drop.Role
		}
		if org == nil {
			org = drop.Organization
		}
		summary := keep.Summary
		if summary == "" {
			summary = drop.Summary
		}
		aliases := without(uniq(append(append(append([]string{}, keep.Aliases...), drop.Name), drop.Aliases...), NameKey, maxAliases), keep.Name)
		emails := uniq(append(append([]string{}, keep.Emails...), drop.Emails...), emailKey, maxContacts)
		handles := uniq(append(append([]string{}, keep.Handles...), drop.Handles...), handleKey, maxContacts)
		manual := 0
		if keep.Manual || drop.Manual {
			manual = 1
		}
		if _, err := tx.Exec(`UPDATE people SET aliases = ?, role = ?, organization = ?, emails = ?, handles = ?, summary = ?, manual = ?, updated_at = ? WHERE id = ? AND workspace_id = ?`,
			listJSON(aliases), role, org, listJSON(emails), listJSON(handles), summary, manual, nowMs(), keepID, workspaceID); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM people WHERE id = ? AND workspace_id = ?`, dropID, workspaceID)
		return err
	})
	if err != nil {
		return Row{}, err
	}
	r, _, err := GetRow(q, workspaceID, keepID)
	return r, err
}

// Delete is deletePerson: the person and their links go, the facts stay.
func Delete(q db.Q, workspaceID, id string) (bool, error) {
	n, err := db.Changes(q.Exec(`DELETE FROM people WHERE id = ? AND workspace_id = ?`, id, workspaceID))
	return n > 0, err
}

// --- merge suggestions ---------------------------------------------------------------

// Suggestion is MergeSuggestion.
type Suggestion struct {
	Keep       Row      `json:"keep"`
	Drop       Row      `json:"drop"`
	Reasons    []string `json:"reasons"`
	Similarity *float64 `json:"similarity"`
}

var nameRank = map[NameMatch]int{Same: 3, Strong: 2, Weak: 1}

// keepFirst: hand-edited, more facts, then the fuller name, then the older row.
func keepFirst(a, b Row) (Row, Row) {
	score := func(p Row) []float64 {
		m := 0.0
		if p.Manual {
			m = 1
		}
		return []float64{m, float64(p.FactCount), float64(len(tokens(p.Name))), -float64(p.CreatedAt)}
	}
	x, y := score(a), score(b)
	for i := range x {
		if x[i] != y[i] {
			if x[i] > y[i] {
				return a, b
			}
			return b, a
		}
	}
	return a, b
}

// SuggestMerges is suggestPeopleMerges: likely duplicates in one container,
// for someone to confirm. limit <= 0 is 50.
func SuggestMerges(q db.Q, scope search.Scope, limit int) ([]Suggestion, error) {
	everyone, err := inScope(q, scope)
	if err != nil {
		return nil, err
	}
	// Rows that read as a team or mailbox get deleted, not merged.
	var all []Row
	for _, p := range everyone {
		var first *string
		if len(p.Emails) > 0 {
			first = &p.Emails[0]
		}
		if !LooksLikeOrganization(p.Name, first) {
			all = append(all, p)
		}
	}
	type info struct{ names, emails, handles []string }
	infos := make([]info, len(all))
	for i, p := range all {
		in := info{names: []string{p.Name}}
		for _, a := range p.Aliases {
			if !isPartialOf(a, p.Name) {
				in.names = append(in.names, a)
			}
		}
		for _, e := range p.Emails {
			if !IsSharedMailbox(e) {
				in.emails = append(in.emails, emailKey(e))
			}
		}
		for _, h := range p.Handles {
			in.handles = append(in.handles, handleKey(h))
		}
		infos[i] = in
	}
	type found struct {
		Suggestion
		check bool
	}
	var list []found
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			a, b := infos[i], infos[j]
			reasons := []string{}
			for _, e := range a.emails {
				if contains(b.emails, e) {
					reasons = append(reasons, "same email "+e)
				}
			}
			for _, h := range a.handles {
				if contains(b.handles, h) {
					reasons = append(reasons, "same handle @"+h)
				}
			}
			var best NameMatch
			for _, x := range a.names {
				for _, y := range b.names {
					if m := MatchNames(x, y); m != "" && (best == "" || nameRank[m] > nameRank[best]) {
						best = m
					}
				}
			}
			check := len(reasons) == 0
			if check {
				if best == "" || (best == Weak && len(a.emails) > 0 && len(b.emails) > 0) {
					continue
				}
				if best == Same {
					reasons = append(reasons, "same name")
				} else {
					reasons = append(reasons, "names look alike")
				}
			}
			keep, drop := keepFirst(all[i], all[j])
			list = append(list, found{Suggestion{Keep: keep, Drop: drop, Reasons: reasons}, check})
		}
	}
	var checked []Row
	seen := map[string]bool{}
	for _, f := range list {
		if !f.check {
			continue
		}
		for _, p := range []Row{f.Keep, f.Drop} {
			if !seen[p.ID] {
				seen[p.ID] = true
				checked = append(checked, p)
			}
		}
	}
	if len(checked) > 0 {
		texts := make([]string, len(checked))
		for i, p := range checked {
			ft, err := factTexts(q, scope.WorkspaceID, p.ID)
			if err != nil {
				return nil, err
			}
			texts[i] = describe(p.Name, p.Role, p.Organization, &p.Summary, ft)
		}
		vectors, err := Embed(texts)
		if err != nil {
			return nil, err
		}
		byID := map[string][]float32{}
		for i, p := range checked {
			byID[p.ID] = vectors[i]
		}
		for k := range list {
			if list[k].check {
				s := cosine(byID[list[k].Keep.ID], byID[list[k].Drop.ID])
				list[k].Similarity = &s
			}
		}
	}
	var kept []found
	for _, f := range list {
		if !f.check || *f.Similarity >= FuzzyPersonSimilarity {
			kept = append(kept, f)
		}
	}
	simOr0 := func(f found) float64 {
		if f.Similarity == nil {
			return 0
		}
		return *f.Similarity
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].check != kept[j].check {
			return !kept[i].check
		}
		return simOr0(kept[j]) < simOr0(kept[i])
	})
	if limit <= 0 {
		limit = 50
	}
	out := []Suggestion{}
	for i, f := range kept {
		if i >= limit {
			break
		}
		out = append(out, f.Suggestion)
	}
	return out, nil
}

// --- ask --------------------------------------------------------------------------

const maxAskPeople = 2

// NamedIn is peopleNamedIn: people in the container whose name or alias
// appears in the text as whole words. A one-word name must be capitalized in
// the text unless the whole text is lowercase.
func NamedIn(q db.Q, scope search.Scope, text string) ([]Row, error) {
	hay := " " + NameKey(text) + " "
	casual := standaloneI.ReplaceAll(text, "i") == strx.Lower(text)
	capitalized := map[string]bool{}
	for _, w := range words(text) {
		if r := []rune(w); len(r) > 0 && strx.IsUpperLetter(r[0]) {
			capitalized[strx.Lower(w)] = true
		}
	}
	named := func(name string) bool {
		key := NameKey(name)
		if strx.Len16(key) < 2 || !strings.Contains(hay, " "+key+" ") {
			return false
		}
		return casual || strings.Contains(key, " ") || capitalized[key]
	}
	all, err := inScope(q, scope)
	if err != nil {
		return nil, err
	}
	type hit struct {
		p   Row
		len int
	}
	var hits []hit
	for _, p := range all {
		best := 0
		for _, n := range append([]string{p.Name}, p.Aliases...) {
			if named(n) {
				best = max(best, strx.Len16(NameKey(n)))
			}
		}
		if best > 0 {
			hits = append(hits, hit{p, best})
		}
	}
	last := func(p Row) int64 {
		if p.LastMentionedAt == nil {
			return 0
		}
		return *p.LastMentionedAt
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].len != hits[j].len {
			return hits[i].len > hits[j].len
		}
		return last(hits[i].p) > last(hits[j].p)
	})
	out := []Row{}
	for i, h := range hits {
		if i >= maxAskPeople {
			break
		}
		out = append(out, h.p)
	}
	return out, nil
}
