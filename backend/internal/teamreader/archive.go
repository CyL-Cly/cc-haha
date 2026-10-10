package teamreader

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/activitystatsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var localeMu sync.Mutex
var localeCollator = collate.New(language.Und)

func localeCompare(a, b string) int {
	localeMu.Lock()
	defer localeMu.Unlock()
	return localeCollator.CompareString(a, b)
}
func numeric(value json.RawMessage) float64 {
	if len(value) == 0 || string(value) == "null" {
		return math.NaN()
	}
	var n float64
	if json.Unmarshal(value, &n) != nil {
		return math.NaN()
	}
	return n
}
func safeInteger(value json.RawMessage) bool {
	n := numeric(value)
	return !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) <= 9007199254740991 && math.Trunc(n) == n
}
func nonblank(value json.RawMessage) bool {
	_, err := protocol.DecodeJavaScriptJSONString(value)
	return err == nil && protocol.TrimJavaScriptString(obj(raw(map[string]json.RawMessage{"s": value})).String("s")) != ""
}

var archiveDates = activitystatsapi.NewIndexDateParser(time.Local)

func dateMillis(text string) float64 {
	value, valid, err := archiveDates.Parse(text)
	if err != nil || !valid {
		return math.NaN()
	}
	return value
}
func (s *Service) ArchivePath(session string) string {
	return filepath.Join(s.options.ConfigDir, "cc-haha", "agent-teams", hash([]byte(session))+".json")
}
func lastSnapshot(entry protocol.JSONObject) protocol.JSONObject {
	snapshots := rows(entry.Get("snapshots"))
	if len(snapshots) == 0 {
		return protocol.JSONObject{}
	}
	return snapshots[len(snapshots)-1]
}
func memberIdentity(member protocol.JSONObject) string {
	if v := member.String("agentId"); v != "" {
		return v
	}
	return member.String("name")
}
func carryMembers(snapshot, previous protocol.JSONObject) protocol.JSONObject {
	if !previous.IsObject() {
		return snapshot.Clone()
	}
	team := obj(snapshot.Get("team"))
	oldTeam := obj(previous.Get("team"))
	current := map[string]protocol.JSONObject{}
	for _, member := range rows(team.Get("members")) {
		current[memberIdentity(member)] = member
	}
	members := []protocol.JSONObject{}
	historical := map[string]bool{}
	for _, old := range rows(oldTeam.Get("members")) {
		id := memberIdentity(old)
		historical[id] = true
		next := old.Clone()
		if live, ok := current[id]; ok {
			next.Assign(live)
			if live.Get("model") == nil && old.Get("model") != nil {
				next.Set("model", old.Get("model"))
			}
		} else {
			status := "completed"
			if old.String("status") == "failed" {
				status = "failed"
			}
			next.Set("status", raw(status))
			next.Set("activity", raw("exited"))
		}
		members = append(members, next)
	}
	for _, member := range rows(team.Get("members")) {
		if !historical[memberIdentity(member)] {
			members = append(members, member)
		}
	}
	active := 0
	for _, member := range members {
		if member.String("status") == "running" {
			active++
		}
	}
	nextTeam := team.Clone()
	nextTeam.Set("memberCount", raw(len(members)))
	nextTeam.Set("activeMemberCount", raw(active))
	nextTeam.Set("members", raw(members))
	if bytes.Equal(protocol.StringifyValidatedJavaScriptJSON(raw(team)), protocol.StringifyValidatedJavaScriptJSON(raw(nextTeam))) {
		return snapshot.Clone()
	}
	next := snapshot.Clone()
	next.Set("team", raw(nextTeam))
	next.Set("version", raw(hash(protocol.StringifyValidatedJavaScriptJSON(raw(protocol.NewJSONObject(field("team", nextTeam), protocol.JSONField{Name: "tasks", Value: snapshot.Get("tasks")}, protocol.JSONField{Name: "messages", Value: snapshot.Get("messages")}, protocol.JSONField{Name: "deletedAt", Value: snapshot.Get("deletedAt")}))))))
	return next
}
func (s *Service) ReadArchive(session string) *protocol.JSONObject {
	data, err := os.ReadFile(s.ArchivePath(session))
	if err != nil {
		return nil
	}
	document := obj(protocol.DecodeJavaScriptUTF8(data))
	if string(document.Get("schemaVersion")) != "1" || document.String("sessionId") != session || !bytes.HasPrefix(bytes.TrimSpace(document.Get("teams")), []byte("[")) {
		return nil
	}
	type archivedGroup struct {
		entry     protocol.JSONObject
		snapshots []protocol.JSONObject
	}
	grouped := map[string]*archivedGroup{}
	order := []string{}
	for _, entry := range rows(document.Get("teams")) {
		if !nonblank(entry.Get("teamName")) || !nonblank(entry.Get("updatedAt")) || !bytes.HasPrefix(bytes.TrimSpace(entry.Get("snapshots")), []byte("[")) {
			continue
		}
		for _, snapshot := range rows(entry.Get("snapshots")) {
			team := obj(snapshot.Get("team"))
			if !snapshot.IsObject() || !team.IsObject() || !nonblank(snapshot.Get("version")) || !nonblank(snapshot.Get("generatedAt")) || !bytes.HasPrefix(bytes.TrimSpace(snapshot.Get("tasks")), []byte("[")) || !bytes.HasPrefix(bytes.TrimSpace(snapshot.Get("messages")), []byte("[")) {
				continue
			}
			id := team.String("incarnationId")
			if !nonblank(team.Get("incarnationId")) {
				identityTeam := team.Clone()
				if !nonblank(team.Get("name")) {
					identityTeam.Set("name", entry.Get("teamName"))
				}
				created := numberValue(team.Get("createdAt"))
				if math.IsNaN(created) || math.IsInf(created, 0) {
					created = 0
				}
				identityTeam.Set("createdAt", raw(created))
				if !nonblank(team.Get("leadSessionId")) {
					identityTeam.Delete("leadSessionId")
				}
				id = incarnation(identityTeam)
			}
			team.Set("incarnationId", raw(id))
			snapshot.Set("team", raw(team))
			key := entry.String("teamName") + "\x00" + id
			existing := grouped[key]
			latest := protocol.JSONObject{}
			if existing != nil && len(existing.snapshots) > 0 {
				latest = existing.snapshots[len(existing.snapshots)-1]
			}
			snapshot = carryMembers(snapshot, latest)
			if existing == nil {
				order = append(order, key)
				existing = &archivedGroup{entry: entry.Clone(), snapshots: []protocol.JSONObject{}}
				grouped[key] = existing
			}
			if !latest.IsObject() || latest.String("version") != snapshot.String("version") {
				existing.snapshots = append(existing.snapshots, snapshot)
			}
			existing.entry.Set("teamName", entry.Get("teamName"))
			existing.entry.Set("incarnationId", raw(id))
			existing.entry.Set("updatedAt", snapshot.Get("generatedAt"))
		}
	}
	teams := []protocol.JSONObject{}
	for _, key := range order {
		grouped[key].entry.Set("snapshots", raw(grouped[key].snapshots))
		teams = append(teams, grouped[key].entry)
	}
	document.Set("schemaVersion", raw(1))
	document.Set("sessionId", raw(session))
	updated := document.Get("updatedAt")
	if !nonblank(updated) {
		updated = raw(time.UnixMilli(0).UTC().Format("2006-01-02T15:04:05.000Z"))
		if len(teams) > 0 {
			updated = teams[len(teams)-1].Get("updatedAt")
		}
	}
	document.Set("updatedAt", updated)
	document.Set("teams", raw(teams))
	return &document
}
func numberValue(value json.RawMessage) float64 {
	value = bytes.TrimSpace(value)
	if len(value) == 0 {
		return math.NaN()
	}
	switch string(value) {
	case "null", "false":
		return 0
	case "true":
		return 1
	}
	if value[0] != '"' && value[0] != '[' && value[0] != '{' {
		return numeric(value)
	}
	quoted, err := protocol.JavaScriptStringJSON(value)
	if err != nil {
		return math.NaN()
	}
	text := protocol.TrimJavaScriptString(obj(raw(map[string]json.RawMessage{"s": quoted})).String("s"))
	if text == "" {
		return 0
	}
	if len(text) > 2 && text[0] == '0' {
		base := 0
		switch text[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base > 0 {
			integer, ok := new(big.Int).SetString(text[2:], base)
			if !ok || strings.ContainsAny(text[2:], "+-_") {
				return math.NaN()
			}
			n, _ := new(big.Float).SetInt(integer).Float64()
			return n
		}
	}
	if strings.ContainsAny(text, "pP_") || text == "Inf" || text == "+Inf" || text == "-Inf" {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil && !math.IsInf(n, 0) {
		return math.NaN()
	}
	return n
}
func archiveCreatedAt(entry protocol.JSONObject) float64 {
	team := obj(lastSnapshot(entry).Get("team"))
	n := numeric(team.Get("createdAt"))
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return n
}
func sortArchiveEntries(entries []protocol.JSONObject, nameTie bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		difference := archiveCreatedAt(right) - archiveCreatedAt(left)
		if difference != 0 {
			return difference < 0
		}
		difference = dateMillis(right.String("updatedAt")) - dateMillis(left.String("updatedAt"))
		if !math.IsNaN(difference) && difference != 0 {
			return difference < 0
		}
		return nameTie && localeCompare(right.String("teamName"), left.String("teamName")) < 0
	})
}
func (s *Service) SelectArchiveEntry(document *protocol.JSONObject, lookup protocol.JSONObject) *protocol.JSONObject {
	if document == nil {
		return nil
	}
	all := rows(document.Get("teams"))
	candidates := []protocol.JSONObject{}
	id, name := lookup.String("incarnationId"), lookup.String("teamName")
	if id != "" {
		for _, entry := range all {
			if entry.String("incarnationId") == id && (name == "" || entry.String("teamName") == name) && len(rows(entry.Get("snapshots"))) > 0 {
				copy := entry.Clone()
				return &copy
			}
		}
		return nil
	}
	at := numeric(lookup.Get("at"))
	for _, entry := range all {
		if len(rows(entry.Get("snapshots"))) == 0 || name != "" && entry.String("teamName") != name {
			continue
		}
		if !math.IsNaN(at) && !math.IsInf(at, 0) {
			start, end := archiveCreatedAt(entry), math.Inf(1)
			deleted := lastSnapshot(entry).String("deletedAt")
			if deleted != "" {
				n := dateMillis(deleted)
				if !math.IsNaN(n) {
					end = n
				}
			}
			for _, other := range all {
				if other.String("teamName") == entry.String("teamName") && other.String("incarnationId") != entry.String("incarnationId") {
					next := archiveCreatedAt(other)
					if next > start && next < end {
						end = next
					}
				}
			}
			if !(start <= at && at < end) {
				continue
			}
		}
		candidates = append(candidates, entry)
	}
	sortArchiveEntries(candidates, false)
	if len(candidates) == 0 {
		return nil
	}
	copy := candidates[0].Clone()
	return &copy
}
func (s *Service) archiveLock(path string) *sync.Mutex {
	s.archiveMu.Lock()
	defer s.archiveMu.Unlock()
	lock := s.archiveLocks[path]
	if lock == nil {
		lock = &sync.Mutex{}
		s.archiveLocks[path] = lock
	}
	return lock
}
func (s *Service) writeArchive(path string, document protocol.JSONObject) error {
	if err := os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return err
	}
	var randomness [6]byte
	if _, err := rand.Read(randomness[:]); err != nil {
		return err
	}
	temporary := fmt.Sprintf("%s.tmp.%d.%d.%s", path, os.Getpid(), s.options.Now().UnixMilli(), hex.EncodeToString(randomness[:]))
	defer os.Remove(temporary)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, protocol.StringifyValidatedJavaScriptJSON(raw(document)), "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')
	if err := os.WriteFile(temporary, pretty.Bytes(), 0666); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
func (s *Service) ArchiveSnapshot(ctx context.Context, snapshot protocol.JSONObject) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	normalized := snapshot.Clone()
	team := obj(normalized.Get("team"))
	if team.String("incarnationId") == "" {
		team.Set("incarnationId", raw(incarnation(team)))
	}
	normalized.Set("team", raw(team))
	session, id := team.String("leadSessionId"), team.String("incarnationId")
	if session == "" {
		return nil
	}
	path := s.ArchivePath(session)
	lock := s.archiveLock(path)
	lock.Lock()
	defer lock.Unlock()
	current := s.ReadArchive(session)
	document := protocol.NewJSONObject(field("schemaVersion", 1), field("sessionId", session), protocol.JSONField{Name: "updatedAt", Value: normalized.Get("generatedAt")}, field("teams", []protocol.JSONObject{}))
	if current != nil {
		document = current.Clone()
	}
	teams := rows(document.Get("teams"))
	index := -1
	existing := protocol.JSONObject{}
	for i, entry := range teams {
		if entry.String("incarnationId") == id {
			index, existing = i, entry
			break
		}
	}
	incoming := carryMembers(normalized, lastSnapshot(existing))
	latest := lastSnapshot(existing)
	incomingAt, latestAt := dateMillis(incoming.String("generatedAt")), dateMillis(latest.String("generatedAt"))
	incomingRevision, latestRevision := numeric(incoming.Get("taskListRevision")), numeric(latest.Get("taskListRevision"))
	comparable := safeInteger(incoming.Get("taskListRevision")) && safeInteger(latest.Get("taskListRevision"))
	if latest.String("deletedAt") != "" && incoming.String("deletedAt") == "" || comparable && incomingRevision < latestRevision || (!comparable || incomingRevision == latestRevision) && !math.IsNaN(incomingAt) && !math.IsNaN(latestAt) && (incomingAt < latestAt || incomingAt == latestAt && latest.String("version") != incoming.String("version")) {
		return nil
	}
	snapshots := rows(existing.Get("snapshots"))
	if latest.IsObject() && latest.String("version") == incoming.String("version") {
		snapshots[len(snapshots)-1] = incoming
	} else {
		snapshots = append(snapshots, incoming)
		if len(snapshots) > 200 {
			snapshots = snapshots[len(snapshots)-200:]
		}
	}
	entry := existing.Clone()
	if !entry.IsObject() {
		entry = protocol.NewJSONObject()
	}
	entry.Set("teamName", team.Get("name"))
	entry.Set("incarnationId", raw(id))
	entry.Set("updatedAt", incoming.Get("generatedAt"))
	entry.Set("snapshots", raw(snapshots))
	if index < 0 {
		teams = append(teams, entry)
	} else {
		teams[index] = entry
	}
	document.Set("schemaVersion", raw(1))
	document.Set("sessionId", raw(session))
	document.Set("updatedAt", normalized.Get("generatedAt"))
	document.Set("teams", raw(teams))
	return s.writeArchive(path, document)
}
