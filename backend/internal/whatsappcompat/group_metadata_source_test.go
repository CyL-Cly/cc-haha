package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
)

type groupSourceNode struct {
	Tag     string            `json:"tag"`
	Attrs   map[string]string `json:"attrs"`
	Content any               `json:"content,omitempty"`
}

func groupWire(t *testing.T, source groupSourceNode) wab.Node {
	t.Helper()
	node := wab.Node{Tag: source.Tag, Attrs: wab.Attrs{}}
	for key, value := range source.Attrs {
		if strings.Contains(value, "@") {
			jid, err := types.ParseJID(value)
			if err != nil {
				t.Fatal(err)
			}
			node.Attrs[key] = jid
		} else {
			node.Attrs[key] = value
		}
	}
	switch content := source.Content.(type) {
	case string:
		node.Content = []byte(content)
	case []groupSourceNode:
		children := make([]wab.Node, len(content))
		for i, child := range content {
			children[i] = groupWire(t, child)
		}
		node.Content = children
	}
	return node
}

func actualGroupMetadata(t *testing.T, rows []groupSourceNode) []map[string]any {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_group_metadata.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Baileys group decoder", err, stderr.String())
	}
	var out []map[string]any
	if err = json.Unmarshal(raw, &out); err != nil || len(out) != len(rows) {
		t.Fatal("original metadata receipt", err, string(raw))
	}
	return out
}

func sharedGroupMetadata(group *types.GroupInfo) map[string]any {
	participants := make([]map[string]any, len(group.Participants))
	for i, participant := range group.Participants {
		participants[i] = map[string]any{"id": participant.JID.String(), "phone": participant.PhoneNumber.String(), "lid": participant.LID.String(), "admin": participant.IsAdmin, "superadmin": participant.IsSuperAdmin}
	}
	value := map[string]any{
		"id": group.JID.String(), "subject": group.Name, "subjectOwner": group.NameSetBy.String(), "subjectOwnerPn": group.NameSetByPN.String(), "subjectTime": group.NameSetAt.Unix(),
		"creation": group.GroupCreated.Unix(), "owner": group.OwnerJID.String(), "ownerPn": group.OwnerPN.String(), "ownerCountryCode": group.CreatorCountryCode,
		"description": group.Topic, "descriptionId": group.TopicID, "descriptionOwner": group.TopicSetBy.String(), "descriptionOwnerPn": group.TopicSetByPN.String(), "descriptionTime": int64(0),
		"linkedParent": group.LinkedParentJID.String(), "locked": group.IsLocked, "announce": group.IsAnnounce, "parent": group.IsParent, "defaultSubGroup": group.IsDefaultSubGroup,
		"approval": group.IsJoinApprovalRequired, "allMemberAdd": group.MemberAddMode == types.GroupMemberAddModeAllMember,
		"ephemeral": group.IsEphemeral, "expiration": group.DisappearingTimer, "size": group.ParticipantCount, "addressingMode": group.AddressingMode, "participants": participants,
	}
	if !group.TopicSetAt.IsZero() {
		value["descriptionTime"] = group.TopicSetAt.Unix()
	}
	// Decode once through JSON so original JS numbers and Go integers have the
	// same observation representation. No production value is rewritten.
	var result map[string]any
	_ = json.Unmarshal(nodeJSON(value), &result)
	return result
}

func groupMetadataRows() []groupSourceNode {
	rows := []groupSourceNode{}
	for mask := 0; mask < 32; mask++ {
		children := []groupSourceNode{
			{"participant", map[string]string{"jid": "111@s.whatsapp.net", "lid": "99111@lid", "type": "admin"}, nil},
			{"participant", map[string]string{"jid": "99222@lid", "phone_number": "222@s.whatsapp.net", "type": "superadmin", "display_name": "+2•••22"}, nil},
			{"participant", map[string]string{"jid": "99333@lid"}, nil},
			{"description", map[string]string{"id": "description-1", "participant": "99222@lid", "participant_pn": "222@s.whatsapp.net", "t": "1700000002"}, []groupSourceNode{{"body", map[string]string{}, "描述🙂\nsecond line"}}},
			{"member_add_mode", map[string]string{}, "all_member_add"},
			{"ephemeral", map[string]string{"expiration": "86400"}, nil},
			{"linked_parent", map[string]string{"jid": "99100-1@g.us"}, nil},
			{"future_metadata", map[string]string{"version": "next"}, "retained wire"},
		}
		for bit, tag := range []string{"locked", "announcement", "parent", "default_sub_group", "membership_approval_mode"} {
			if mask&(1<<bit) != 0 {
				children = append(children, groupSourceNode{tag, map[string]string{}, nil})
			}
		}
		rows = append(rows, groupSourceNode{"group", map[string]string{"id": "99000-" + strconv.Itoa(mask+1), "subject": "group🙂", "s_o": "99222@lid", "s_o_pn": "222@s.whatsapp.net", "s_t": "1700000001", "creation": "1700000000", "creator": "99111@lid", "creator_pn": "111@s.whatsapp.net", "creator_country_code": "1", "size": "3", "addressing_mode": "lid"}, children})
	}
	rows = append(rows, groupSourceNode{"group", map[string]string{"id": "99000-minimal", "subject": "minimal", "s_t": "1700000001", "creation": "1700000000", "size": "0", "addressing_mode": "pn"}, []groupSourceNode{}})
	return rows
}

func TestActualOriginalGroupMetadataParticipantsRolesAndCommunityFlags(t *testing.T) {
	rows := groupMetadataRows()
	source := actualGroupMetadata(t, rows)
	client := whatsmeow.NewClient(&store.Device{}, nil)
	for i, row := range rows {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			node := groupWire(t, row)
			before := node.XMLString()
			group, err := client.DangerousInternals().ParseGroupNode(&node)
			if err != nil {
				t.Fatal(err)
			}
			actual := sharedGroupMetadata(group)
			if !reflect.DeepEqual(actual, source[i]) {
				t.Fatalf("actual original group metadata differs\nGo: %s\nSource: %s", nodeJSON(actual), nodeJSON(source[i]))
			}
			if node.XMLString() != before {
				t.Fatal("parsing changed the original wire node")
			}
		})
	}
}
