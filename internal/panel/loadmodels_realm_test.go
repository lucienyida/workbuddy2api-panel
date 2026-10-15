package panel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runLoadModels 在 Node 的 VM 里跑真实的 loadModels（从 app.js 摘出该函数），
// 用桩 api() 驱动，返回记录到的请求路径与合并后的模型 id 列表。
//
// failRealms 里的域对应的请求会抛错（模拟该域没有可用账号时端点返回 503）。
func runLoadModels(t *testing.T, failRealms ...string) (calls []string, models []string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; loadModels merge test skipped")
	}

	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');
const failRealms = JSON.parse(process.argv[3] || '[]') || [];

const start = src.indexOf('async function loadModels');
const end = src.indexOf('function renderModels');
if (start < 0 || end < 0) throw new Error('loadModels not found');

const calls = [];
const fake = {
  'models':              { models: [{ id: 'cn:glm-5.2' }, { id: 'cn:auto' }] },
  'models?realm=global': { models: [{ id: 'global:hy3' }] },
  'model_probes':        { probes: {} },
};

const els = {};
const el = id => (els[id] = els[id] || { innerHTML: '', textContent: '', children: [], addEventListener(){}, onclick: null });
const ctx = {
  console, JSON, Math, Date, Number, String, Boolean, Object, Array, Promise, Map, Set,
  RegExp, Error, TypeError, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  api: async p => {
    calls.push(p);
    // 该域没有可用账号 → 端点 503 → api() 抛错（与真实前端 api() 行为一致）。
    if (p === 'models' && failRealms.includes('cn')) throw new Error('HTTP 503: 没有可用的 cn 账号');
    if (p === 'models?realm=global' && failRealms.includes('global')) throw new Error('HTTP 503: 没有可用的 global 账号');
    if (p in fake) return fake[p];
    throw new Error('unexpected api ' + p);
  },
  $: el,
  esc: s => String(s),
  renderModels: () => {},
};
vm.createContext(ctx);
// mdAll 是模块级 let；摘出来的片段要能读写它，必须与 loadModels 同在顶层作用域，
// 并挂到 ctx 上供宿主观测（直接跑片段的话赋值会落在片段自己的绑定里）。
vm.runInContext(
  'var mdAll = [], mdProbes = {}, mdProbeOf = () => undefined;\n' +
  src.slice(start, end) +
  '\nthis.loadModels = loadModels; this.readMdAll = () => mdAll;',
  ctx);

ctx.loadModels().then(() => {
  process.stdout.write(JSON.stringify({
    calls: calls,
    models: (ctx.readMdAll() || []).map(m => m.id),
    err: null,
  }));
}).catch(e => {
  // 未捕获 → Promise.all 整体失败，页面将空白
  process.stdout.write(JSON.stringify({ calls: calls, models: [], err: String(e && e.message || e) }));
});`

	f, err := os.CreateTemp(t.TempDir(), "loadmodels-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// json.Marshal(nil slice) → "null"，JSON.parse("null") → null，
	// 脚本里 failRealms.includes(...) 会抛 TypeError。必须显式发 "[]"。
	if failRealms == nil {
		failRealms = []string{}
	}
	payload, _ := json.Marshal(failRealms)
	absApp, err := filepath.Abs("app.js")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, f.Name(), absApp, string(payload)).CombinedOutput()
	if err != nil {
		t.Fatalf("loadModels node test failed: %v\n%s", err, out)
	}

	var res struct {
		Calls  []string `json:"calls"`
		Models []string `json:"models"`
		Err    string   `json:"err"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &res); err != nil {
		t.Fatalf("parse node output: %v\n%s", err, out)
	}
	if res.Err != "" {
		t.Fatalf("loadModels 整体失败（页面将空白）：%s；请求=%v", res.Err, res.Calls)
	}
	return res.Calls, res.Models
}

// 「模型与档位」页必须同时展示国内版与国际版模型。
//
// 回归背景：27190a0 把 /panel/api/models 改成按 realm 分域返回（默认 cn），
// 白名单编辑器依赖该契约。但「模型与档位」是全量总览页，它的 loadModels()
// 当时只调了不带 realm 的 api('models')，于是**国际版账号的模型整体消失**。
//
// 本测试钉住：loadModels 必须分别请求两个域并合并，而不是只取默认域。
// 只断言"调用了 models"是不够的——那正是出 bug 时的状态。
func TestAppJSLoadModelsMergesBothRealms(t *testing.T) {
	calls, models := runLoadModels(t)

	if !contains(calls, "models?realm=global") {
		t.Fatalf("loadModels 未请求国际版模型（只取默认域）：%v", calls)
	}
	if !contains(models, "global:hy3") {
		t.Fatalf("国际版模型未出现在列表中：%v", models)
	}
	if !contains(models, "cn:glm-5.2") {
		t.Fatalf("国内版模型丢失：%v", models)
	}
}

// 单域不可用时，另一域的模型必须照常显示。
//
// 回归背景（用户实测三连）：
//   - 禁用国内账号 → 国际版模型也不显示，整页空白
//   - 禁用国际账号 → 国内模型正常显示
//   - 全部禁用     → 整页空白
//
// 根因：两个请求里只有 global 挂了 .catch()，cn 的 503 异常冒泡出 Promise.all，
// 把整页带崩。可用域是各自的局部状态，不该互相拖死 —— 两侧都必须独立容错。
func TestAppJSLoadModelsDegradesPerRealm(t *testing.T) {
	t.Run("国内不可用_国际仍显示", func(t *testing.T) {
		_, models := runLoadModels(t, "cn")
		if !contains(models, "global:hy3") {
			t.Fatalf("国内域失败时国际版模型被拖死（整页空白）：%v", models)
		}
	})
	t.Run("国际不可用_国内仍显示", func(t *testing.T) {
		_, models := runLoadModels(t, "global")
		if !contains(models, "cn:glm-5.2") {
			t.Fatalf("国际域失败时国内模型被拖死：%v", models)
		}
	})
	t.Run("两个都不可用_不崩但为空", func(t *testing.T) {
		_, models := runLoadModels(t, "cn", "global")
		if len(models) != 0 {
			t.Fatalf("两域皆不可用时应为空列表，得到：%v", models)
		}
	})
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
