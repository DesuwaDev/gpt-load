package control

import (
	"encoding/json"
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/storage/models"
)

// 时效起点只服务于界面上的 1 小时倒计时：换值时重置，只改模型名单时保持不变，
// 清空注入值时归零。这条时间线错了倒计时就会骗人，所以按状态机逐步走一遍。
func TestCredentialTurnStateSetAtTracksValueChangesOnly(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("turn state ttl"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "turn-state-secret", ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.CodexTurnStateSetAtMS != 0 {
		t.Fatalf("fresh credential set_at = %d, want 0", row.CodexTurnStateSetAtMS)
	}

	clock := row.UpdatedAtMS + 1000
	fixture.service.now = func() time.Time { return time.UnixMilli(clock) }
	patch := func(request CredentialUpdateRequest) CredentialItemResponse {
		t.Helper()
		item, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID, request)
		if err != nil {
			t.Fatalf("UpdateGroupCredential() error = %v", err)
		}
		return item
	}

	first := patch(CredentialUpdateRequest{
		CodexTurnState: optionalField[string]{Set: true, Value: "state-one"},
	})
	if first.CodexTurnState != "state-one" || first.CodexTurnStateSetAtMS != clock {
		t.Fatalf("after first write = %#v", first)
	}

	// 只改模型名单不该冲掉倒计时——注入值还是同一个，年龄也还是同一个。
	clock += 1000
	scoped := patch(CredentialUpdateRequest{
		CodexTurnStateModels: optionalField[string]{Set: true, Value: "GPT-5.1-Codex"},
	})
	if scoped.CodexTurnStateModels != "gpt-5.1-codex" || scoped.CodexTurnStateSetAtMS != first.CodexTurnStateSetAtMS {
		t.Fatalf("after models-only write = %#v", scoped)
	}

	// 原样重提同一个值也不重置：它还是上游那时候签发的那一个 state。
	clock += 1000
	resubmitted := patch(CredentialUpdateRequest{
		CodexTurnState: optionalField[string]{Set: true, Value: "state-one"},
	})
	if resubmitted.CodexTurnStateSetAtMS != first.CodexTurnStateSetAtMS {
		t.Fatalf("after resubmitting the same value = %#v", resubmitted)
	}

	// 换成新值才重置。
	clock += 1000
	replaced := patch(CredentialUpdateRequest{
		CodexTurnState: optionalField[string]{Set: true, Value: "state-two"},
	})
	if replaced.CodexTurnState != "state-two" || replaced.CodexTurnStateSetAtMS != clock {
		t.Fatalf("after replacing the value = %#v", replaced)
	}

	// 清空注入值时起点一并归零，界面就不会对着一个空值继续倒数。
	clock += 1000
	cleared := patch(CredentialUpdateRequest{
		CodexTurnState: optionalField[string]{Set: true, Null: true},
	})
	if cleared.CodexTurnState != "" || cleared.CodexTurnStateSetAtMS != 0 {
		t.Fatalf("after clearing the value = %#v", cleared)
	}
	var committed models.Credential
	if err := fixture.db.Take(&committed, row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if committed.CodexTurnState != "" || committed.CodexTurnStateSetAtMS != 0 {
		t.Fatalf("committed row = %#v", committed)
	}
}

// 0023 之前写进去的注入值没有起点，界面只能显示「时效未知」；重新保存同一个值时补
// 一次起点，让倒计时能从这一刻开始走，而不是逼用户先清空再粘贴一遍。
func TestCredentialTurnStateSetAtBackfillsLegacyRowOnResave(t *testing.T) {
	t.Parallel()
	fixture := newServiceFixture(t)
	created, err := fixture.service.CreateGroup(t.Context(), GroupCreateRequest{
		Name: stringPointer("turn state legacy"), ChannelID: channel.OpenAI,
		Params: json.RawMessage(`{}`), Models: optionalGroupModels{Set: true},
		Credentials: "legacy-secret", ConnectionType: "api_key",
	})
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	var row models.Credential
	if err := fixture.db.Where("group_id = ?", created.GroupID).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	// 直接改库模拟存量行：有注入值，没有起点。
	if err := fixture.db.Model(&models.Credential{}).Where("id = ?", row.ID).
		Updates(map[string]any{"codex_turn_state": "legacy-state", "codex_turn_state_set_at_ms": 0}).
		Error; err != nil {
		t.Fatal(err)
	}
	listed, err := fixture.service.loadCredentialItem(t.Context(), created.GroupID, row.ID)
	if err != nil {
		t.Fatalf("loadCredentialItem() error = %v", err)
	}
	if listed.CodexTurnState != "legacy-state" || listed.CodexTurnStateSetAtMS != 0 {
		t.Fatalf("legacy row presentation = %#v", listed)
	}

	clock := row.UpdatedAtMS + 1000
	fixture.service.now = func() time.Time { return time.UnixMilli(clock) }
	resaved, err := fixture.service.UpdateGroupCredential(t.Context(), created.GroupID, row.ID,
		CredentialUpdateRequest{CodexTurnState: optionalField[string]{Set: true, Value: "legacy-state"}})
	if err != nil {
		t.Fatalf("UpdateGroupCredential() error = %v", err)
	}
	if resaved.CodexTurnState != "legacy-state" || resaved.CodexTurnStateSetAtMS != clock {
		t.Fatalf("after re-saving the legacy value = %#v", resaved)
	}
}
