package whatsappcandidate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	wab "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
)

type groupActionSourceRow struct {
	Name      string            `json:"name"`
	Method    string            `json:"method"`
	Args      []any             `json:"args"`
	Responses []groupSourceNode `json:"responses"`
}

type groupActionSourceReceipt struct {
	Requests           []json.RawMessage `json:"requests"`
	EncodedRequests    [][]byte          `json:"encodedRequests"`
	Value              json.RawMessage   `json:"value"`
	RemainingResponses int               `json:"remainingResponses"`
}

func groupActionRows(jid string) []groupActionSourceRow {
	empty := groupSourceNode{Tag: "iq", Attrs: map[string]string{}}
	rows := []groupActionSourceRow{
		{"subject-unicode", "groupUpdateSubject", []any{jid, "描述🙂\nsecond line"}, []groupSourceNode{empty}},
		{"leave", "groupLeave", []any{jid}, []groupSourceNode{empty}},
	}
	for _, setting := range []string{"locked", "unlocked", "announcement", "not_announcement"} {
		rows = append(rows, groupActionSourceRow{setting, "groupSettingUpdate", []any{jid, setting}, []groupSourceNode{empty}})
	}
	for _, mode := range []string{"on", "off"} {
		rows = append(rows, groupActionSourceRow{"approval-" + mode, "groupJoinApprovalMode", []any{jid, mode}, []groupSourceNode{empty}})
	}
	for _, mode := range []string{"admin_add", "all_member_add"} {
		rows = append(rows, groupActionSourceRow{"member-add-" + mode, "groupMemberAddMode", []any{jid, mode}, []groupSourceNode{empty}})
	}
	for _, method := range []string{"groupInviteCode", "groupRevokeInvite"} {
		response := groupSourceNode{Tag: "iq", Attrs: map[string]string{}, Content: []groupSourceNode{{Tag: "invite", Attrs: map[string]string{"code": "private-invite-code"}}}}
		rows = append(rows, groupActionSourceRow{method, method, []any{jid}, []groupSourceNode{response}})
	}
	rows = append(rows, groupActionSourceRow{"accept-invite", "groupAcceptInvite", []any{"private-invite-code"}, []groupSourceNode{{Tag: "iq", Attrs: map[string]string{}, Content: []groupSourceNode{{Tag: "group", Attrs: map[string]string{"jid": jid}}}}}})
	participants := []string{"555@s.whatsapp.net", "556@s.whatsapp.net"}
	for _, action := range []string{"add", "remove", "promote", "demote", "approve", "reject"} {
		method, tag := "groupParticipantsUpdate", action
		responseAction := groupSourceNode{Tag: action, Attrs: map[string]string{}, Content: []groupSourceNode{
			{Tag: "participant", Attrs: map[string]string{"jid": participants[0]}},
			{Tag: "participant", Attrs: map[string]string{"jid": participants[1], "error": "403"}},
		}}
		if action == "approve" || action == "reject" {
			method, tag = "groupRequestParticipantsUpdate", "membership_requests_action"
			responseAction = groupSourceNode{Tag: tag, Attrs: map[string]string{}, Content: []groupSourceNode{responseAction}}
		}
		rows = append(rows, groupActionSourceRow{action, method, []any{jid, participants, action}, []groupSourceNode{{Tag: "iq", Attrs: map[string]string{}, Content: []groupSourceNode{responseAction}}}})
	}
	return rows
}

func actualGroupActions(t *testing.T, rows []groupActionSourceRow) []groupActionSourceReceipt {
	t.Helper()
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", nodeSourceFixturePath("node_group_actions.fixture.ts"))
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WHATSAPP_LEAF=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(nodeJSON(rows))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal("actual original group actions", err, stderr.String())
	}
	var receipts []groupActionSourceReceipt
	if err = json.Unmarshal(raw, &receipts); err != nil || len(receipts) != len(rows) {
		t.Fatal("actual original group action receipts", err, string(raw))
	}
	for i, receipt := range receipts {
		if len(receipt.Requests) != len(rows[i].Responses) || len(receipt.EncodedRequests) != len(receipt.Requests) || receipt.RemainingResponses != 0 {
			t.Fatal("actual original group action did not consume exact query responses", rows[i].Name, receipt)
		}
	}
	return receipts
}

func groupActionPhysicalNode(node wab.Node, iq bool) any {
	attrs := map[string]string{}
	for key, value := range node.Attrs {
		if iq && key == "id" {
			continue // The real transport generates and matches its own IQ identity.
		}
		attrs[key] = fmt.Sprint(value)
	}
	value := map[string]any{"tag": node.Tag, "attrs": attrs}
	switch content := node.Content.(type) {
	case []byte:
		value["content"] = string(content)
	case string:
		value["content"] = content
	case []wab.Node:
		if len(content) > 0 {
			children := make([]any, len(content))
			for i, child := range content {
				children[i] = groupActionPhysicalNode(child, false)
			}
			value["content"] = children
		}
	}
	return value
}

func runGroupAction(ctx context.Context, client *whatsmeow.Client, jid types.JID, row groupActionSourceRow) (any, error) {
	switch row.Method {
	case "groupUpdateSubject":
		return nil, client.SetGroupName(ctx, jid, row.Args[1].(string))
	case "groupLeave":
		return nil, client.LeaveGroup(ctx, jid)
	case "groupSettingUpdate":
		switch row.Args[1].(string) {
		case "locked", "unlocked":
			return nil, client.SetGroupLocked(ctx, jid, row.Args[1] == "locked")
		default:
			return nil, client.SetGroupAnnounce(ctx, jid, row.Args[1] == "announcement")
		}
	case "groupJoinApprovalMode":
		return nil, client.SetGroupJoinApprovalMode(ctx, jid, row.Args[1] == "on")
	case "groupMemberAddMode":
		return nil, client.SetGroupMemberAddMode(ctx, jid, types.GroupMemberAddMode(row.Args[1].(string)))
	case "groupInviteCode", "groupRevokeInvite":
		value, err := client.GetGroupInviteLink(ctx, jid, row.Method == "groupRevokeInvite")
		if err != nil {
			return nil, err
		}
		return value[len(whatsmeow.InviteLinkPrefix):], nil
	case "groupAcceptInvite":
		value, err := client.JoinGroupWithLink(ctx, row.Args[0].(string))
		return value.String(), err
	case "groupParticipantsUpdate", "groupRequestParticipantsUpdate":
		var participants []types.JID
		for _, participant := range row.Args[1].([]string) {
			value, err := types.ParseJID(participant)
			if err != nil {
				return nil, err
			}
			participants = append(participants, value)
		}
		var result []types.GroupParticipant
		var err error
		if row.Method == "groupParticipantsUpdate" {
			result, err = client.UpdateGroupParticipants(ctx, jid, participants, whatsmeow.ParticipantChange(row.Args[2].(string)))
		} else {
			result, err = client.UpdateGroupRequestParticipants(ctx, jid, participants, whatsmeow.ParticipantRequestChange(row.Args[2].(string)))
		}
		if err != nil {
			return nil, err
		}
		values := make([]map[string]string, len(result))
		for i, participant := range result {
			status := "200"
			if participant.Error != 0 {
				status = strconv.Itoa(participant.Error)
			}
			values[i] = map[string]string{"jid": participant.JID.String(), "status": status}
		}
		return values, nil
	}
	return nil, fmt.Errorf("unknown bounded group action %q", row.Method)
}
