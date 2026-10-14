package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ruilian/internal/branding"
	"ruilian/internal/licensecode"
	"ruilian/internal/platform"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		if len(os.Args) == 1 {
			platform.Message("发行方授权管理工具", e.Error())
		}
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "keygen":
			return keygen(os.Args[2:])
		case "issue":
			return issue(os.Args[2:])
		case "check-key":
			return checkKey(os.Args[2:])
		case "help", "--help", "-h":
			fmt.Println("keygen --private <私钥路径> --public <公钥路径>\nissue --key <私钥路径> --installation <安装ID> --customer <客户编号> --from 1.1.0 --to 1.9999.9999 --out <授权文件>\n不带参数启动图形授权工具。")
			return nil
		}
	}
	return gui()
}
func checkKey(args []string) error {
	f := flag.NewFlagSet("check-key", flag.ContinueOnError)
	key := f.String("key", "issuer-private.pem", "发行方私钥路径")
	publicPath := f.String("public", "public.key", "客户端公钥路径")
	if e := f.Parse(args); e != nil {
		return e
	}
	raw, e := os.ReadFile(*key)
	if e != nil {
		return e
	}
	private, e := licensecode.PrivateKey(raw)
	if e != nil {
		return e
	}
	publicRaw, e := os.ReadFile(*publicPath)
	if e != nil {
		return e
	}
	public, e := licensecode.PublicKey(publicRaw)
	if e != nil {
		return e
	}
	if !bytes.Equal(private.Public().(ed25519.PublicKey), public) {
		return fmt.Errorf("发行方私钥与客户端公钥不匹配，禁止构建不兼容的授权版本")
	}
	hash := sha256.Sum256(public)
	fmt.Printf("公私钥匹配。公钥指纹：%x\n", hash)
	return nil
}
func writeNew(path string, data []byte) error {
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e == nil {
		e = ce
	}
	return e
}
func keygen(args []string) error {
	f := flag.NewFlagSet("keygen", flag.ContinueOnError)
	private := f.String("private", "issuer-private.pem", "私钥输出文件")
	public := f.String("public", "public.key", "公钥输出文件")
	if e := f.Parse(args); e != nil {
		return e
	}
	if _, e := os.Stat(*private); !os.IsNotExist(e) {
		return fmt.Errorf("私钥文件已存在，禁止覆盖：%s", *private)
	}
	pr, pu, e := licensecode.Generate()
	if e != nil {
		return e
	}
	if e = writeNew(*private, pr); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*public), 0700); e != nil {
		return e
	}
	if e = os.WriteFile(*public, pu, 0644); e != nil {
		return e
	}
	fmt.Println("密钥生成完成。私钥只能保存在发行方环境，请勿分发给客户。")
	return nil
}
func issue(args []string) error {
	f := flag.NewFlagSet("issue", flag.ContinueOnError)
	key := f.String("key", "issuer-private.pem", "发行方私钥")
	installation := f.String("installation", "", "客户安装ID")
	customer := f.String("customer", "", "客户编号")
	from := f.String("from", "1.1.0", "最低版本")
	to := f.String("to", "1.9999.9999", "最高版本")
	out := f.String("out", "", "授权文件")
	features := f.String("features", "*", "功能，逗号分隔")
	if e := f.Parse(args); e != nil {
		return e
	}
	raw, e := os.ReadFile(*key)
	if e != nil {
		return e
	}
	private, e := licensecode.PrivateKey(raw)
	if e != nil {
		return e
	}
	p := licensecode.NewPayload(*customer, *installation, *from, *to, strings.Split(*features, ","))
	if e = licensecode.VerifyPayload(p, *installation, "1.1.0"); e != nil {
		return e
	}
	token, e := licensecode.Encode(p, private)
	if e != nil {
		return e
	}
	if *out != "" {
		if e = writeNew(*out, []byte(token)); e != nil {
			return e
		}
		fmt.Println("授权文件已生成：" + *out)
	} else {
		fmt.Println(token)
	}
	return nil
}

func gui() error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	keyPath := filepath.Join(filepath.Dir(exe), "issuer-private.pem")
	raw, e := os.ReadFile(keyPath)
	if e != nil {
		return fmt.Errorf("找不到发行方私钥：%s\n请将原始 issuer-private.pem 放到本工具旁。重新生成密钥需要重新构建客户端", keyPath)
	}
	private, e := licensecode.PrivateKey(raw)
	if e != nil {
		return e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer listener.Close()
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	csrf := hex.EncodeToString(b)
	address := "http://" + listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/fonts/NotoSansSC.ttf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "font/ttf")
		http.ServeContent(w, r, "NotoSansSC.ttf", time.Time{}, bytes.NewReader(issuerFont))
	})
	t := template.Must(template.New("issuer").Parse(issuerHTML))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("token") != csrf {
			http.Error(w, "授权工具访问凭证无效，请重新启动工具", 403)
			return
		}
		if r.Method == "GET" {
			_ = t.Execute(w, map[string]string{"Token": csrf, "AppName": branding.Default.AppName})
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		origin, er := url.Parse(r.Header.Get("Origin"))
		if er != nil || origin.Host != r.Host {
			http.Error(w, "跨站请求被拒绝", 403)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 20000)
		if er = r.ParseForm(); er != nil {
			http.Error(w, "输入过长", 400)
			return
		}
		p := licensecode.NewPayload(strings.TrimSpace(r.FormValue("customer")), strings.TrimSpace(r.FormValue("installation")), strings.TrimSpace(r.FormValue("from")), strings.TrimSpace(r.FormValue("to")), []string{"*"})
		data := map[string]string{"Token": csrf, "AppName": branding.Default.AppName, "Customer": p.CustomerID, "Installation": p.InstallationID}
		if len(p.InstallationID) != 32 || len(p.CustomerID) > 200 {
			data["Error"] = "安装标识须为 32 位，客户编号最多 200 字节"
		} else if er = licensecode.VerifyPayload(p, p.InstallationID, "1.1.0"); er != nil {
			data["Error"] = er.Error()
		} else {
			token, er := licensecode.Encode(p, private)
			if er != nil {
				data["Error"] = er.Error()
			} else {
				data["Code"] = token
				data["LicenseID"] = p.LicenseID
				record, _ := json.Marshal(p)
				f, er := os.OpenFile(filepath.Join(filepath.Dir(exe), "issued-licenses.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if er == nil {
					_, _ = f.Write(append(record, '\n'))
					_ = f.Close()
				}
			}
		}
		_ = t.Execute(w, data)
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	if e = platform.Browser(address + "/?token=" + csrf); e != nil {
		return e
	}
	fmt.Println("授权管理工具已启动，请妥善保管私钥。关闭终端可结束工具。")
	return server.Serve(listener)
}

//go:embed assets/NotoSansSC.ttf
var issuerFont []byte

const issuerHTML = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.AppName}} · 发行方授权管理</title><style>@font-face{font-family: 'Noto Sans SC';src:url(/fonts/NotoSansSC.ttf);font-weight:100 900}*{box-sizing:border-box}body{font:15px 'Noto Sans SC',sans-serif;background:#eff3f9;color:#22324b;margin:0;padding:40px 20px}main{max-width:800px;margin:auto;background:white;padding:38px 44px;border-radius:18px;box-shadow:0 16px 50px #14243e10}small{color:#5378a6;letter-spacing:2px}h1{font-size:28px;margin:12px 0}p{color:#67758b;line-height:1.8}label{display:block;margin:20px 0 8px;font-weight:600}input,textarea{width:100%;border:1px solid #d8e1ed;border-radius:8px;padding:12px;font:inherit}textarea{height:190px;font:12px 'Noto Sans SC',sans-serif;word-break:break-all;background:#f6f9ff}button{border:0;background:#2874ed;color:white;border-radius:8px;padding:13px 26px;margin-top:24px;font:inherit;cursor:pointer}.pair{display:flex;gap:18px}.pair>div{flex:1}.notice{background:#fff6e6;border:1px solid #ffe5b2;border-radius:8px;padding:12px;color:#966519}.result{background:#f0faf4;border:1px solid #ccebd8;padding:16px;border-radius:8px;margin-top:24px}.error{color:#c43838}</style><main><small>{{.AppName}} · LICENSE ISSUER</small><h1>永久授权签发</h1><p>为指定客户与安装标识生成离线永久授权。公钥已内置于配套客户端。</p><div class="notice">本工具和 issuer-private.pem 仅供发行方保管，不能放入客户安装包。授权记录保存于 issued-licenses.jsonl。</div><form method="post" action="/?token={{.Token}}"><label>客户编号</label><input name="customer" value="{{.Customer}}" placeholder="例如：KH-2026-001" required maxlength="200"><label>Installation ID</label><input name="installation" value="{{.Installation}}" placeholder="粘贴客户软件“系统管理 → 软件授权”中的安装标识" required minlength="32" maxlength="32"><div class="pair"><div><label>起始版本</label><input name="from" value="1.1.0" required></div><div><label>最高版本</label><input name="to" value="1.9999.9999" required></div></div><button>生成永久授权码</button></form>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}{{if .Code}}<div class="result"><strong>授权已生成 · {{.LicenseID}}</strong><p>将以下授权码完整复制给客户，或保存为 .license 文本文件供客户导入。</p><textarea readonly onclick="this.select()">{{.Code}}</textarea></div>{{end}}<p style="font-size:12px;margin-bottom:0">授权类型：PERPETUAL · 全部功能 · Ed25519 数字签名</p></main></html>`
