// panel_webauth.go 把「面板账密登录（web_auth）」接入 linguo 的鉴权链。
//
// 设计约束（本次移植的硬性要求）：
//   1. **不修改 linguo 现有的 withAuth**——它的函数体一字未动。
//   2. web_auth 未启用时，行为与移植前**完全一致**（零回归）。
//   3. web_auth 启用时，语义对齐 qiaoba223 的实现：只认会话 Cookie、
//      Bearer 一律拒绝、改状态请求过 CSRF 双提交校验。
//
// 做法：新增一层 withAuthOrWeb 包装，内部按 web_auth 开关二选一：
//
//	未启用 → 直接透传给 linguo 原 withAuth（api_key 模式，原封不动）
//	已启用 → 走账密模式（session cookie + CSRF），逻辑逐行对齐 qiaoba
//
// 路由注册处把 p.withAuth 换成 p.withAuthOrWeb 即可，其余不动。
// 来源: qiaoba223/workbuddy2api-panel（web_login.go / csrf.go / withAuth 账密分支）
package panel

import (
	"net/http"
)

// withAuthOrWeb 鉴权入口：web_auth 未启用时**透传** linguo 原 withAuth。
//
// 注意：这里刻意不复制 linguo withAuth 的分支逻辑，而是直接调用它，
// 这样日后 linguo 若调整 api_key 语义（例如支持多密钥），本包装自动跟随，
// 不会出现"两份 api_key 校验逻辑各自漂移"的问题。
func (p *Panel) withAuthOrWeb(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 未启用（含 WebAuth == nil）→ 完全走 linguo 原路径，零回归。
		if !p.cfg.WebAuth.Enabled() {
			p.withAuth(next)(w, r)
			return
		}
		// 账密模式：只认会话 Cookie（Bearer 一律拒绝）。
		if _, ok := p.cfg.WebAuth.Valid(r); !ok {
			writeErr(w, http.StatusUnauthorized, "not_logged_in")
			return
		}
		// 改状态请求需过 CSRF 双提交校验（见 csrf.go）。
		if !checkCSRF(r) {
			writeErr(w, http.StatusForbidden, "csrf token missing or mismatched")
			return
		}
		next(w, r)
	}
}

// withAuthPublic 公开端点（无需登录）：登录 / 首次初始化 / 状态查询。
//
// 这些端点必须能匿名访问，否则无法完成"首次建管理员"与"登录"本身。
// 它们各自在 handler 内部做安全约束（如 authSetup 仅在用户库为空时可用）。
func (p *Panel) withAuthPublic(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r)
	}
}
