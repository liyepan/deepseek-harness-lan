package cli

import (
	"fmt"
	"io"
	"strings"

	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/dsh"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/lan"
)

type bannerData struct {
	listen    string
	upstream  string
	port      int
	token     string
	addresses []lan.Address
	spawned   *dsh.Process
}

// banner prints everything the user needs: where to point a phone and what the
// exposure costs. It deliberately never prints the launch token.
func banner(w io.Writer, d bannerData) {
	label := func(k, v string) string {
		return fmt.Sprintf("  %-8s%s\n", k, v)
	}

	fmt.Fprintf(w, "\ndsh-lan %s\n\n", Version)
	fmt.Fprint(w, label("上游", "http://"+d.upstream))
	fmt.Fprint(w, label("监听", d.listen+"  (所有网卡)"))
	if d.spawned != nil {
		fmt.Fprint(w, label("dsh", d.spawned.CommandLine()))
	} else {
		fmt.Fprint(w, label("dsh", "复用已在运行的实例"))
	}

	if len(d.addresses) == 0 {
		fmt.Fprint(w, "\n  局域网地址\n    未发现非回环网卡；只能在 127.0.0.1 上访问\n")
	} else {
		fmt.Fprint(w, "\n  局域网地址（任何一块网卡都可用）\n")
		for _, a := range d.addresses {
			fmt.Fprintf(w, "    http://%s:%d/  (%s)\n", a.IP, d.port, a.Iface)
		}
	}

	fmt.Fprintln(w)
	if d.token == "" {
		fmt.Fprint(w, "  注意  没有读到 dsh 的启动令牌，首次访问需要手动完成认证\n\n")
	}

	fmt.Fprint(w, "  安全提示\n")
	fmt.Fprint(w, "    能打开这个页面的人，就能在这台机器上执行命令、读写文件。\n")
	fmt.Fprint(w, "    只在可信局域网里开，不要把端口映射到公网。\n")
	fmt.Fprint(w, "    Ctrl+C 退出")
	if d.spawned != nil {
		fmt.Fprint(w, "，dsh 会一起停")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w)
}

func shortToken(t string) string {
	if len(t) <= 8 {
		return t
	}
	return t[:4] + "..." + t[len(t)-4:]
}

func trimSpace(s string) string { return strings.TrimSpace(s) }
