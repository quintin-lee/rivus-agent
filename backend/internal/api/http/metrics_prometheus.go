package httpapi

import (
	"expvar"
	"fmt"
	"net/http"
	"strings"
)

// handlePromMetrics 以 Prometheus 文本格式暴露 expvar 指标。
// 顶层 Int/Float 输出为 counter；Map 按 key 展开为 {key="..."} 标签；
// 其余类型（String/Func/JSON，如默认注册表中的 cmdline/memstats）跳过。
func (s *Server) handlePromMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	expvar.Do(func(kv expvar.KeyValue) {
		switch v := kv.Value.(type) {
		case *expvar.Int:
			fmt.Fprintf(&b, "# TYPE %s counter\n%s %d\n", kv.Key, kv.Key, v.Value())
		case *expvar.Float:
			fmt.Fprintf(&b, "# TYPE %s counter\n%s %v\n", kv.Key, kv.Key, v.Value())
		case *expvar.Map:
			v.Do(func(e expvar.KeyValue) {
				fmt.Fprintf(&b, "# TYPE %s counter\n%s{key=%q} ", kv.Key, kv.Key, e.Key)
				switch ev := e.Value.(type) {
				case *expvar.Int:
					fmt.Fprintf(&b, "%d\n", ev.Value())
				case *expvar.Float:
					fmt.Fprintf(&b, "%v\n", ev.Value())
				default:
					b.WriteString("0\n")
				}
			})
		}
	})
	_, _ = w.Write([]byte(b.String()))
}
