package tool

import "strings"

// Remove 按名称删除工具。返回是否存在该工具。
func (r *Registry) Remove(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.tools[name]
	delete(r.tools, name)
	return ok
}

// RemoveByPrefix 删除所有名称以 prefix 开头的工具。返回删除数量。
func (r *Registry) RemoveByPrefix(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k := range r.tools {
		if strings.HasPrefix(k, prefix) {
			delete(r.tools, k)
			n++
		}
	}
	return n
}

// Replace 清除所有以 prefix 开头的工具，再通过 Register 重新注册 defs
// （TimeoutMs/MaxResultBytes 默认值与重名校验保持单一来源）。
// 返回首次注册错误。
// 注意：非原子——RemoveByPrefix 与 Register 循环各自独立加锁，中间窗口内并发
// goroutine 可能看到已被清除的 prefix。单线程 reconcile 路径（ToolConfigProvider）安全。
// 空 prefix 匹配所有名称，等同清空整个 registry。
func (r *Registry) Replace(prefix string, defs []Definition) error {
	r.RemoveByPrefix(prefix)
	for i := range defs {
		if err := r.Register(defs[i]); err != nil {
			return err
		}
	}
	return nil
}
