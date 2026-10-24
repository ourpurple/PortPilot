package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/http"
	"strings"
)

func preProxyConfigName(id string) string { return "pre-" + id }

func hasPreProxy(st *State, id string) bool {
	if id == "" {
		return true // Empty means connect directly to the original node.
	}
	for _, p := range st.PreProxies {
		if p.ID == id {
			return true
		}
	}
	return false
}

func (n *Node) clearPreProxyResults() {
	n.Latency, n.TestedAt, n.TestError = nil, nil, ""
	n.clearExitIP()
}

func parsePreProxy(content string) (map[string]any, error) {
	proxies, err := parseProxies([]byte(content))
	if err != nil {
		return nil, err
	}
	if len(proxies) != 1 {
		return nil, fmt.Errorf("前置代理只能包含一个节点")
	}
	p := proxies[0]
	// JSON normalizes the numeric types produced by YAML and persisted state.
	data, err := json.Marshal(p["port"])
	var port float64
	if err != nil || json.Unmarshal(data, &port) != nil || port < 1 || port > 65535 || math.Trunc(port) != port {
		return nil, fmt.Errorf("前置代理端口必须为 1–65535 的整数")
	}
	p["port"] = int(port)
	if _, err := json.Marshal(p); err != nil {
		return nil, fmt.Errorf("前置代理包含不支持的配置字段")
	}
	return p, nil
}

func samePreProxyConfig(a, b map[string]any) bool {
	a, b = maps.Clone(a), maps.Clone(b)
	// The generated name depends only on the ID, not the input/display name.
	delete(a, "name")
	delete(b, "name")
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}

func (a *App) savePreProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 200 {
		fail(w, 400, "前置代理名称不能为空且最多 200 字节")
		return
	}
	config, err := parsePreProxy(body.Content)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	id := r.PathValue("id")
	err = a.store.Update(func(st *State) error {
		for _, p := range st.PreProxies {
			if p.ID != id && p.Name == body.Name {
				return fmt.Errorf("前置代理名称已存在")
			}
		}
		if id == "" {
			st.PreProxies = append(st.PreProxies, PreProxy{ID: randomID(), Name: body.Name, Config: config})
			st.event("success", "已添加前置代理："+body.Name)
			return nil
		}
		for i := range st.PreProxies {
			p := &st.PreProxies[i]
			if p.ID != id {
				continue
			}
			changed := !samePreProxyConfig(p.Config, config)
			if !changed && p.Name == body.Name {
				return nil
			}
			p.Name, p.Config = body.Name, config
			referenced := false
			if changed {
				for j := range st.Nodes {
					if st.Nodes[j].PreProxyID == id {
						st.Nodes[j].clearPreProxyResults()
						referenced = true
					}
				}
			}
			if referenced {
				st.Revision++
			}
			st.event("info", "已更新前置代理："+body.Name)
			return nil
		}
		return fmt.Errorf("前置代理不存在")
	})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	respond(w, status, a.store.Snapshot())
}

func (a *App) deletePreProxy(w http.ResponseWriter, r *http.Request) {
	err := a.store.Update(func(st *State) error {
		id := r.PathValue("id")
		for i, p := range st.PreProxies {
			if p.ID != id {
				continue
			}
			count := 0
			for j := range st.Nodes {
				if st.Nodes[j].PreProxyID == id {
					st.Nodes[j].PreProxyID = ""
					st.Nodes[j].clearPreProxyResults()
					count++
				}
			}
			st.PreProxies = append(st.PreProxies[:i], st.PreProxies[i+1:]...)
			if count > 0 {
				st.Revision++
			}
			st.event("info", fmt.Sprintf("已删除前置代理：%s，解除 %d 个节点绑定", p.Name, count))
			return nil
		}
		return fmt.Errorf("前置代理不存在")
	})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, 200, a.store.Snapshot())
}

func (a *App) batchPreProxy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs        []string `json:"ids"`
		PreProxyID *string  `json:"preProxyId"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if len(body.IDs) == 0 || body.PreProxyID == nil {
		fail(w, 400, "请选择节点并明确指定前置代理；空字符串表示解除绑定")
		return
	}
	err := a.store.Update(func(st *State) error {
		if !hasPreProxy(st, *body.PreProxyID) {
			return fmt.Errorf("前置代理不存在")
		}
		ids := map[string]bool{}
		for _, id := range body.IDs {
			ids[id] = true
		}
		count := 0
		for i := range st.Nodes {
			n := &st.Nodes[i]
			if !ids[n.ID] {
				continue
			}
			delete(ids, n.ID)
			if n.PreProxyID != *body.PreProxyID {
				n.PreProxyID = *body.PreProxyID
				n.clearPreProxyResults()
				count++
			}
		}
		if len(ids) != 0 {
			return fmt.Errorf("所选节点不存在，请重新选择")
		}
		if count > 0 {
			st.Revision++
			st.event("info", fmt.Sprintf("已更新 %d 个节点的前置代理绑定", count))
		}
		return nil
	})
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	respond(w, 200, a.store.Snapshot())
}
