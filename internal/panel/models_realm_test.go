package panel

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lucienyida/workbuddy2api-panel/internal/auth"
	"github.com/lucienyida/workbuddy2api-panel/internal/pool"
)

// TestModelsRealmScoped 钉住 /panel/api/models 的**分域契约**：
// 默认（不传 realm）= 只返回 cn 模型；?realm=global 只返回 global 模型。
//
// 为什么需要：白名单编辑器按 tab（国内/国际）分别渲染 modelCache.cn / modelCache.global。
// 早期实现忽略 realm 参数、把两域合并成一份列表返回，两个 tab 拿到同一份混合数据，
// 表现为「勾选国内版出现国内国际、勾选国际版也一样」——分不开。此测试防回归。
func TestModelsRealmScoped(t *testing.T) {
	p := pool.New("")
	p.Add(&auth.Auth{UID: "cn-1", AccessToken: "at1", Domain: "copilot.tencent.com"})
	p.Add(&auth.Auth{UID: "gl-1", AccessToken: "at3", Domain: "workbuddy.ai"})

	up := fakeModelsUpstream(map[string][]string{
		"/console/enterprises/personal/models": {"deepseek-v4.1-flash"},
		"/v2/enterprises/personal/models":      {"gemini-3.5-flash"},
	})
	up.GlobalEnabled = true
	pn := New(Config{Pool: p, Upstream: up, APIKey: "k"})

	type resp struct {
		OK     bool `json:"ok"`
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}

	idsOf := func(path string) []string {
		t.Helper()
		rec := httptestGet(t, pn, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, rec.Code, rec.Body.String())
		}
		var out resp
		json.Unmarshal(rec.Body.Bytes(), &out)
		ids := make([]string, 0, len(out.Models))
		for _, m := range out.Models {
			ids = append(ids, m.ID)
		}
		return ids
	}

	// 默认 = cn 域：必须全是 cn: 前缀，且不得混入 global:。
	cnIDs := idsOf("/panel/api/models")
	if len(cnIDs) == 0 {
		t.Fatal("默认 models 返回空，期望 cn 模型")
	}
	for _, id := range cnIDs {
		if !hasPrefix(id, "cn:") {
			t.Errorf("默认（cn）返回了非 cn 模型: %q（两域未分离）", id)
		}
	}

	// ?realm=global = global 域：必须全是 global: 前缀。
	glIDs := idsOf("/panel/api/models?realm=global")
	if len(glIDs) == 0 {
		t.Fatal("realm=global 返回空，期望 global 模型")
	}
	for _, id := range glIDs {
		if !hasPrefix(id, "global:") {
			t.Errorf("realm=global 返回了非 global 模型: %q（两域未分离）", id)
		}
	}

	// 两域内容不得相同（否则说明仍在合并返回）。
	if len(cnIDs) == len(glIDs) && cnIDs[0] == glIDs[0] {
		t.Errorf("cn 与 global 返回同一份列表，未按域分离: %v", cnIDs)
	}
}

// TestModelsRealmScopedMissingRealmAccount 验证某域无账号时只影响该域，
// 不回退去查另一域（否则白名单 tab 又会串味）。
func TestModelsRealmScopedMissingRealmAccount(t *testing.T) {
	p := pool.New("")
	p.Add(&auth.Auth{UID: "cn-1", AccessToken: "at1", Domain: "copilot.tencent.com"})

	up := fakeModelsUpstream(map[string][]string{
		"/console/enterprises/personal/models": {"deepseek-v4.1-flash"},
		"/v2/enterprises/personal/models":      {"gemini-3.5-flash"},
	})
	up.GlobalEnabled = true
	pn := New(Config{Pool: p, Upstream: up, APIKey: "k"})

	// 无 global 账号 → 该域应为「没有可用的 global 账号」，而不是回退返回 cn 模型。
	rec := httptestGet(t, pn, "/panel/api/models?realm=global")
	if rec.Code == http.StatusOK {
		var out struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		for _, m := range out.Models {
			if hasPrefix(m.ID, "cn:") {
				t.Fatalf("global 域无账号时却返回了 cn 模型: %q", m.ID)
			}
		}
	}
}

func hasPrefix(s, p string) bool {
	return len(s) >= len(p) && s[:len(p)] == p
}
