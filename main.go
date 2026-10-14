package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type server struct {
	uploadDir string
	maxBytes  int64
	page      *template.Template
}

type uploadResult struct {
	Original string `json:"original"`
	SavedAs  string `json:"savedAs"`
	Size     int64  `json:"size"`
}

type uploadResponse struct {
	OK      bool           `json:"ok"`
	Files   []uploadResult `json:"files,omitempty"`
	Message string         `json:"message"`
}

var errUploadTooLarge = errors.New("upload too large")

func main() {
	addr := flag.String("addr", ":8080", "监听地址，例如 :8080")
	dir := flag.String("dir", "uploads", "文件保存目录")
	maxMiB := flag.Int64("max-upload", 0, "单次请求的最大总大小（MiB），0 表示不限制")
	flag.Parse()

	if *maxMiB < 0 {
		log.Fatal("-max-upload 不能小于 0")
	}
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		log.Fatalf("创建上传目录失败: %v", err)
	}

	s := &server{
		uploadDir: absDir,
		maxBytes:  *maxMiB << 20,
		page:      template.Must(template.New("index").Parse(indexHTML)),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("POST /upload", s.upload)

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	log.Printf("文件将保存到: %s", absDir)
	log.Printf("本机打开: http://localhost%s", displayPort(*addr))
	for _, ip := range localIPv4s() {
		log.Printf("手机打开: http://%s%s", ip, displayPort(*addr))
	}
	log.Fatal(httpServer.ListenAndServe())
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	maxLabel := "单次大小不限"
	if s.maxBytes > 0 {
		maxLabel = fmt.Sprintf("单次最多 %d MiB", s.maxBytes>>20)
	}
	data := struct{ MaxLabel string }{MaxLabel: maxLabel}
	if err := s.page.Execute(w, data); err != nil {
		log.Printf("render page: %v", err)
	}
}

func (s *server) upload(w http.ResponseWriter, r *http.Request) {
	if s.maxBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.maxBytes+(32<<20))
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, uploadResponse{Message: "请求格式不正确"})
		return
	}

	results := make([]uploadResult, 0)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				s.removeResults(results)
				writeJSON(w, http.StatusRequestEntityTooLarge, uploadResponse{Message: fmt.Sprintf("文件总大小超过 %d MiB", s.maxBytes>>20)})
				return
			}
			s.removeResults(results)
			writeJSON(w, http.StatusBadRequest, uploadResponse{Message: "读取上传内容失败"})
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			part.Close()
			continue
		}

		used := int64(0)
		for _, result := range results {
			used += result.Size
		}
		remaining := int64(-1)
		if s.maxBytes > 0 {
			remaining = s.maxBytes - used
		}
		result, err := s.savePart(part, remaining)
		part.Close()
		if err != nil {
			if errors.Is(err, errUploadTooLarge) {
				s.removeResults(results)
				writeJSON(w, http.StatusRequestEntityTooLarge, uploadResponse{Message: fmt.Sprintf("文件总大小超过 %d MiB", s.maxBytes>>20)})
				return
			}
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				s.removeResults(results)
				writeJSON(w, http.StatusRequestEntityTooLarge, uploadResponse{Message: fmt.Sprintf("文件总大小超过 %d MiB", s.maxBytes>>20)})
				return
			}
			log.Printf("save upload: %v", err)
			s.removeResults(results)
			writeJSON(w, http.StatusInternalServerError, uploadResponse{Message: "保存文件失败"})
			return
		}
		results = append(results, result)
	}

	if len(results) == 0 {
		writeJSON(w, http.StatusBadRequest, uploadResponse{Message: "没有收到文件"})
		return
	}
	writeJSON(w, http.StatusOK, uploadResponse{
		OK:      true,
		Files:   results,
		Message: fmt.Sprintf("已成功上传 %d 个文件", len(results)),
	})
}

func (s *server) savePart(part *multipart.Part, remaining int64) (uploadResult, error) {
	original := part.FileName()
	name := safeFilename(original)
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)

	var dst *os.File
	var savedName string
	var err error
	for i := 0; i < 10000; i++ {
		savedName = name
		if i > 0 {
			savedName = fmt.Sprintf("%s (%d)%s", base, i, ext)
		}
		dst, err = os.OpenFile(filepath.Join(s.uploadDir, savedName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return uploadResult{}, err
		}
	}
	if dst == nil {
		return uploadResult{}, errors.New("too many files with the same name")
	}

	var size int64
	var copyErr error
	if remaining < 0 {
		size, copyErr = io.Copy(dst, part)
	} else {
		size, copyErr = io.Copy(dst, io.LimitReader(part, remaining+1))
	}
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst.Name())
		return uploadResult{}, errors.Join(copyErr, closeErr)
	}
	if remaining >= 0 && size > remaining {
		_ = os.Remove(dst.Name())
		return uploadResult{}, errUploadTooLarge
	}
	return uploadResult{Original: original, SavedAs: savedName, Size: size}, nil
}

func (s *server) removeResults(results []uploadResult) {
	for _, result := range results {
		if err := os.Remove(filepath.Join(s.uploadDir, result.SavedAs)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("cleanup upload: %v", err)
		}
	}
}

func safeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '/' || r == '\\' || r == ':' {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "upload"
	}
	// Most filesystems cap a single filename at 255 bytes. Leave room for a
	// numeric suffix used when a file with the same name already exists.
	if len(name) > 220 {
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		limit := 220 - len(ext)
		if limit < 1 {
			ext = ""
			limit = 220
		}
		for len(base) > limit {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		name = base + ext
	}
	return name
}

func writeJSON(w http.ResponseWriter, status int, value uploadResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:")
		next.ServeHTTP(w, r)
	})
}

func displayPort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	_, port, err := net.SplitHostPort(addr)
	if err == nil {
		return ":" + port
	}
	return ":8080"
}

func localIPv4s() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var result []string
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() || ipNet.IP.To4() == nil {
			continue
		}
		result = append(result, ipNet.IP.String())
	}
	return result
}

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
  <meta name="theme-color" content="#10241d">
  <title>传文件</title>
  <style>
    :root{color-scheme:dark;--bg:#081510;--card:#10241d;--line:#29483c;--text:#f4f7ee;--muted:#a5b7ad;--accent:#a7f36b;--accent2:#54d6a1;--danger:#ff9c91}
    *{box-sizing:border-box}body{margin:0;min-height:100dvh;background:radial-gradient(circle at 80% 0,#1b3a2e 0,transparent 35%),var(--bg);color:var(--text);font:16px/1.5 system-ui,-apple-system,"PingFang SC","Microsoft YaHei",sans-serif;padding:max(20px,env(safe-area-inset-top)) max(16px,env(safe-area-inset-right)) max(24px,env(safe-area-inset-bottom)) max(16px,env(safe-area-inset-left));display:grid;place-items:center}
    main{width:min(100%,620px)}.eyebrow{margin:0 0 8px;color:var(--accent);font-size:.8rem;font-weight:800;letter-spacing:.15em;text-transform:uppercase}h1{font-size:clamp(2rem,9vw,4.5rem);line-height:.95;letter-spacing:-.055em;margin:0 0 14px}header>p{color:var(--muted);margin:0 0 28px}
    .card{background:color-mix(in srgb,var(--card) 94%,transparent);border:1px solid var(--line);border-radius:24px;padding:18px;box-shadow:0 22px 70px #0006}.drop{display:block;border:1.5px dashed #527264;border-radius:17px;padding:38px 18px;text-align:center;cursor:pointer;transition:.2s;background:#0b1b15}.drop.drag,.drop:focus-within{border-color:var(--accent);background:#12291f;transform:translateY(-2px)}.icon{display:grid;place-items:center;width:56px;height:56px;border-radius:50%;margin:0 auto 14px;background:var(--accent);color:#102014;font-size:28px;font-weight:900}.drop strong{display:block;font-size:1.1rem}.drop span{display:block;color:var(--muted);font-size:.88rem;margin-top:4px}input[type=file]{position:absolute;width:1px;height:1px;opacity:0}
    .summary{display:flex;justify-content:space-between;gap:12px;margin:18px 2px 10px;font-size:.9rem;color:var(--muted)}.files{list-style:none;padding:0;margin:0;max-height:210px;overflow:auto}.files li{display:flex;gap:10px;align-items:center;padding:10px 4px;border-top:1px solid #ffffff12}.file-name{min-width:0;flex:1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.file-size{color:var(--muted);font-size:.82rem;white-space:nowrap}
    button{width:100%;border:0;border-radius:14px;margin-top:16px;padding:15px 18px;background:linear-gradient(120deg,var(--accent),var(--accent2));color:#0a2117;font:inherit;font-weight:850;cursor:pointer}button:disabled{opacity:.45;cursor:not-allowed}.progress{height:8px;margin-top:16px;border-radius:10px;background:#07100d;overflow:hidden;display:none}.bar{height:100%;width:0;background:var(--accent);transition:width .15s}.status{min-height:24px;margin:14px 2px 0;color:var(--muted)}.status.ok{color:var(--accent)}.status.error{color:var(--danger)}footer{text-align:center;color:#71847a;font-size:.78rem;margin-top:18px}
    @media (max-width:440px){.card{padding:13px;border-radius:20px}.drop{padding:30px 14px}h1{font-size:3.15rem}}
    @media (prefers-reduced-motion:reduce){*{transition:none!important}}
  </style>
</head>
<body>
<main>
  <header><p class="eyebrow">PHONE → COMPUTER</p><h1>把文件传过来。</h1><p>选择照片、视频或文档，可一次上传多个。</p></header>
  <section class="card" aria-label="文件上传">
    <label class="drop" id="drop">
      <span class="icon" aria-hidden="true">↑</span>
      <strong>点这里选择文件</strong>
      <span>也可以把文件拖到这里</span>
      <input id="picker" type="file" name="files" multiple>
    </label>
    <div class="summary"><span id="count">尚未选择</span><span>{{.MaxLabel}}</span></div>
    <ul class="files" id="files"></ul>
    <div class="progress" id="progress" aria-hidden="true"><div class="bar" id="bar"></div></div>
    <button id="send" type="button" disabled>开始上传</button>
    <p class="status" id="status" role="status" aria-live="polite"></p>
  </section>
  <footer>请确保手机与这台电脑处于同一网络</footer>
</main>
<script>
const picker=document.querySelector('#picker'),drop=document.querySelector('#drop'),list=document.querySelector('#files'),count=document.querySelector('#count'),send=document.querySelector('#send'),status=document.querySelector('#status'),progress=document.querySelector('#progress'),bar=document.querySelector('#bar');
let chosen=[];
const size=n=>n<1024?n+' B':n<1048576?(n/1024).toFixed(1)+' KB':n<1073741824?(n/1048576).toFixed(1)+' MB':(n/1073741824).toFixed(1)+' GB';
function render(files){chosen=Array.from(files);list.textContent='';chosen.forEach(f=>{const li=document.createElement('li'),name=document.createElement('span'),bytes=document.createElement('span');name.className='file-name';name.textContent=f.name;bytes.className='file-size';bytes.textContent=size(f.size);li.append(name,bytes);list.append(li)});const total=chosen.reduce((n,f)=>n+f.size,0);count.textContent=chosen.length?chosen.length+' 个文件 · '+size(total):'尚未选择';send.disabled=!chosen.length;status.textContent='';status.className='status'}
picker.addEventListener('change',()=>render(picker.files));
['dragenter','dragover'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.add('drag')}));
['dragleave','drop'].forEach(e=>drop.addEventListener(e,x=>{x.preventDefault();drop.classList.remove('drag')}));
drop.addEventListener('drop',e=>render(e.dataTransfer.files));
send.addEventListener('click',()=>{if(!chosen.length)return;const body=new FormData();chosen.forEach(f=>body.append('files',f));const xhr=new XMLHttpRequest();xhr.open('POST','/upload');send.disabled=true;send.textContent='正在上传…';progress.style.display='block';bar.style.width='0';status.textContent='请保持页面打开';xhr.upload.onprogress=e=>{if(e.lengthComputable)bar.style.width=Math.round(e.loaded/e.total*100)+'%'};xhr.onload=()=>{let data={};try{data=JSON.parse(xhr.responseText)}catch{};if(xhr.status>=200&&xhr.status<300){bar.style.width='100%';status.textContent=data.message||'上传完成';status.className='status ok';chosen=[];picker.value='';list.textContent='';count.textContent='尚未选择';send.textContent='再传一些';send.disabled=true}else{status.textContent=data.message||'上传失败，请重试';status.className='status error';send.textContent='重新上传';send.disabled=false}};xhr.onerror=()=>{status.textContent='网络连接中断，请重试';status.className='status error';send.textContent='重新上传';send.disabled=false};xhr.send(body)});
</script>
</body>
</html>`
