package cli

import (
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/dsh"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/lan"
	"git.linuxacme.com/liyepan/deepseek-harness-lan/internal/state"
)

func runDoctor(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(errw)
	listen := fs.String("listen", defaultListen, "address the gateway would bind")
	upstream := fs.String("upstream", defaultUpstream, "dsh web address")
	statePath := fs.String("state", defaultState, "token cache path")
	fs.Usage = func() { usage(errw) }
	if err := fs.Parse(args); err != nil {
		return 2
	}

	problems := 0
	report := func(ok bool, name, detail string) {
		mark := "OK  "
		if !ok {
			mark = "FAIL"
			problems++
		}
		fmt.Fprintf(out, "  [%s] %-14s %s\n", mark, name, detail)
	}
	warn := func(name, detail string) {
		fmt.Fprintf(out, "  [WARN] %-14s %s\n", name, detail)
	}

	fmt.Fprintf(out, "\ndsh-lan %s doctor\n\n", Version)

	if path, err := dsh.LookPath("dsh"); err != nil {
		warn("dsh", err.Error())
	} else {
		report(true, "dsh", path)
	}

	if dsh.UpstreamUp(*upstream, 500*time.Millisecond) {
		report(true, "上游 "+*upstream, describeUpstream(*upstream))
	} else {
		report(false, "上游 "+*upstream, "没有监听；运行 dsh-lan 时会自动启动 dsh")
	}

	if ln, err := net.Listen("tcp", *listen); err != nil {
		report(false, "监听 "+*listen, err.Error())
	} else {
		_ = ln.Close()
		report(true, "监听 "+*listen, "可用")
	}

	addrs := lan.Addresses()
	if len(addrs) == 0 {
		report(false, "局域网网卡", "没有找到非回环 IPv4 地址")
	} else {
		parts := make([]string, 0, len(addrs))
		for _, a := range addrs {
			tag := "public"
			if a.Private {
				tag = "private"
			}
			parts = append(parts, fmt.Sprintf("%s (%s, %s)", a.IP, a.Iface, tag))
		}
		report(true, "局域网网卡", fmt.Sprint(len(addrs))+" 个: "+joinLines(parts, "; "))
	}

	if st, err := state.Load(*statePath); err != nil {
		warn("令牌缓存", err.Error())
	} else if st.Token == "" {
		warn("令牌缓存", "还没有；首次启动 dsh 时会自动保存")
	} else {
		report(true, "令牌缓存", shortToken(st.Token)+"  from "+*statePath)
	}

	fmt.Fprint(out, "\n  防火墙\n")
	fmt.Fprint(out, "    如果本机能访问、手机不能，多半是防火墙没放行。以 ufw 为例：\n")
	fmt.Fprintf(out, "      sudo ufw allow %d/tcp\n", portOf(*listen))

	fmt.Fprintln(out)
	if problems > 0 {
		fmt.Fprintf(out, "  %d 项需要处理。\n\n", problems)
		return 1
	}
	fmt.Fprint(out, "  环境看起来没问题。\n\n")
	return 0
}

// describeUpstream performs one loopback GET and explains the status.
func describeUpstream(addr string) string {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		return "TCP 可连，但 HTTP 无响应: " + err.Error()
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return "HTTP 200，已认证或无需认证"
	case http.StatusUnauthorized:
		return "HTTP 401，正常（需要令牌）"
	case http.StatusForbidden:
		return "HTTP 403，Host/Origin 栅栏拒绝了请求"
	default:
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

func runToken(args []string, out, errw io.Writer) int {
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	fs.SetOutput(errw)
	statePath := fs.String("state", defaultState, "token cache path")
	fs.Usage = func() { usage(errw) }
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() > 0 && fs.Arg(0) == "clear" {
		if err := state.Clear(*statePath); err != nil {
			fmt.Fprintf(errw, "dsh-lan: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "已清空 %s\n", *statePath)
		return 0
	}

	st, err := state.Load(*statePath)
	if err != nil {
		fmt.Fprintf(errw, "dsh-lan: %v\n", err)
		return 1
	}
	if st.Token == "" {
		fmt.Fprintf(out, "还没有缓存的令牌（%s）\n", *statePath)
		return 0
	}
	fmt.Fprintf(out, "%s\n", st.Token)
	return 0
}

func portOf(addr string) int {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		var n int
		if _, scanErr := fmt.Sscan(p, &n); scanErr == nil {
			return n
		}
	}
	return 8080
}

func joinLines(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += sep + p
	}
	return out
}
