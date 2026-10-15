package panel

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// 「模型与档位」页必须同时展示国内版与国际版模型。
//
// 回归背景：27190a0 把 /panel/api/models 改成按 realm 分域返回（默认 cn），
// 白名单编辑器依赖该契约。但「模型与档位」是全量总览页，它的 loadModels()
// 当时只调了不带 realm 的 api('models')，于是**国际版账号的模型整体消失**。
//
// 本测试钉住：loadModels 必须分别请求两个域并合并，而不是只取默认域。
// 只断言"调用了 models"是不够的——那正是出 bug 时的状态。
func TestAppJSLoadModelsMergesBothRealms(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; loadModels merge test skipped")
	}

	script := `const fs = require('fs');
const vm = require('vm');
const src = fs.readFileSync(process.argv[2], 'utf8');

const start = src.indexOf('async function loadModels');
const end = src.indexOf('function renderModels');
if (start < 0 || end < 0) throw new Error('loadModels not found');

// 记录每次 api() 的路径，并按路径返回不同域的模型。
const calls = [];
const fake = {
  'models':                    { models: [{ id: 'cn:glm-5.2' }, { id: 'cn:auto' }] },
  'models?realm=global':       { models: [{ id: 'global:hy3' }] },
  'model_probes':              { probes: {} },
};

const els = {};
const el = id => (els[id] = els[id] || { innerHTML: '', textContent: '', children: [], addEventListener(){}, onclick: null });
const ctx = {
  console, JSON, Math, Date, Number, String, Boolean, Object, Array, Promise, Map, Set,
  RegExp, Error, TypeError, isNaN, parseInt, parseFloat, encodeURIComponent, decodeURIComponent,
  api: async p => { calls.push(p); if (p in fake) return fake[p]; throw new Error('unexpected api ' + p); },
  $: el,
  esc: s => String(s),
  renderModels: () => {},
};
vm.createContext(ctx);
vm.runInContext(src.slice(start, end) + '\nthis.loadModels = loadModels;', ctx);

ctx.loadModels().then(() => {
  process.stdout.write(JSON.stringify({
    calls: calls,
    models: (ctx.mdAll || []).map(m => m.id),
  }));
}).catch(e => { console.error(e); process.exit(1); });`

	f, err := os.CreateTemp(t.TempDir(), "loadmodels-*.cjs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(script); err != nil {
		t.Fatal(err)
	}
	f.Close()

	out, err := exec.Command(node, f.Name(), "app.js").CombinedOutput()
	if err != nil {
		t.Fatalf("loadModels node test failed: %v\n%s", err, out)
	}

	got := strings.TrimSpace(string(out))
	// 必须同时请求两个域
	if !strings.Contains(got, `"models?realm=global"`) {
		t.Fatalf("loadModels 未请求国际版模型（只取默认域）：%s", got)
	}
	// 合并后两个域的模型都要在列表里
	if !strings.Contains(got, `"global:hy3"`) {
		t.Fatalf("国际版模型未出现在列表中：%s", got)
	}
	if !strings.Contains(got, `"cn:glm-5.2"`) {
		t.Fatalf("国内版模型丢失：%s", got)
	}
}
