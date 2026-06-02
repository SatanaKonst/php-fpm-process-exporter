package main

import (
	"bufio"
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var (
	configPath     = flag.String("config", "/etc/php-fpm-process-exporter.json", "path to JSON config file")
	listenAddr     = flag.String("listen", ":9254", "HTTP listen address")
	procRoot       = flag.String("proc-root", "/proc", "proc filesystem root")
	includeThreads = flag.Bool("include-threads", false, "export per-thread CPU metrics")
	healthPath     = flag.String("health-path", "/healthz", "health endpoint path")
	metricsPath    = flag.String("metrics-path", "/metrics", "metrics endpoint path")
)

var (
	masterTitleRE = regexp.MustCompile(`php-fpm:?\s*master process\s*\((.+)\)`)
	workerTitleRE = regexp.MustCompile(`php-fpm:?\s*pool\s+(.+)`)
)

type procInfo struct {
	PID           int
	PPID          int
	UID           int
	User          string
	Title         string
	Exe           string
	Role          string
	WorkerPool    string
	MasterConfig  string
	MasterPID     int
	CPUSeconds    float64
	ResidentBytes uint64
	VirtualBytes  uint64
	Threads       int
}

type threadInfo struct {
	PID        int
	TID        int
	Title      string
	CPUSeconds float64
}

var userCache sync.Map
var appConfig = defaultConfig()

type Config struct {
	Listen         string          `json:"listen"`
	ProcRoot       string          `json:"proc_root"`
	IncludeThreads bool            `json:"include_threads"`
	HealthPath     string          `json:"health_path"`
	MetricsPath    string          `json:"metrics_path"`
	BasicAuth      BasicAuthConfig `json:"basic_auth"`
}

type BasicAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func main() {
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	applyFlagOverrides(&cfg)
	if err := validateConfig(cfg); err != nil {
		log.Fatalf("invalid config: %v", err)
	}
	appConfig = cfg

	mux := http.NewServeMux()
	mux.HandleFunc(appConfig.MetricsPath, metricsHandler)
	mux.HandleFunc(appConfig.HealthPath, healthHandler)

	srv := &http.Server{Addr: appConfig.Listen, Handler: authMiddleware(mux)}
	log.Printf("listening on %s, proc root=%s, includeThreads=%v", appConfig.Listen, appConfig.ProcRoot, appConfig.IncludeThreads)
	log.Fatal(srv.ListenAndServe())
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	procs, err := collectPhpFpmProcesses(appConfig.ProcRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].PID < procs[j].PID })

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	writeProcessMetricsHeader(bw)
	for _, p := range procs {
		writeProcessMetrics(bw, p)
	}

	if appConfig.IncludeThreads {
		writeThreadMetricsHeader(bw)
		threads, err := collectThreadMetrics(appConfig.ProcRoot, procs)
		if err != nil {
			_, _ = fmt.Fprintf(bw, "# exporter error: %v\n", err)
			return
		}
		sort.Slice(threads, func(i, j int) bool {
			if threads[i].PID == threads[j].PID {
				return threads[i].TID < threads[j].TID
			}
			return threads[i].PID < threads[j].PID
		})
		for _, t := range threads {
			writeThreadMetric(bw, t)
		}
	}
}

func defaultConfig() Config {
	return Config{
		Listen:         ":9254",
		ProcRoot:       "/proc",
		IncludeThreads: false,
		HealthPath:     "/healthz",
		MetricsPath:    "/metrics",
	}
}

func loadConfig(path string) (Config, error) {
	cfg := defaultConfig()
	if strings.TrimSpace(path) == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

func applyFlagOverrides(cfg *Config) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "listen":
			cfg.Listen = *listenAddr
		case "proc-root":
			cfg.ProcRoot = *procRoot
		case "include-threads":
			cfg.IncludeThreads = *includeThreads
		case "health-path":
			cfg.HealthPath = *healthPath
		case "metrics-path":
			cfg.MetricsPath = *metricsPath
		}
	})
}

func validateConfig(cfg Config) error {
	userSet := strings.TrimSpace(cfg.BasicAuth.Username) != ""
	passSet := strings.TrimSpace(cfg.BasicAuth.Password) != ""
	if userSet != passSet {
		return fmt.Errorf("basic_auth.username and basic_auth.password must be set together")
	}
	return nil
}

func authMiddleware(next http.Handler) http.Handler {
	if !appConfig.BasicAuth.Enabled() {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !basicAuthMatches(username, password, appConfig.BasicAuth) {
			w.Header().Set("WWW-Authenticate", `Basic realm="php-fpm-process-exporter", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func basicAuthMatches(username, password string, cfg BasicAuthConfig) bool {
	if !cfg.Enabled() {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(username), []byte(cfg.Username)) != 1 {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(cfg.Password)) != 1 {
		return false
	}
	return true
}

func (c BasicAuthConfig) Enabled() bool {
	return strings.TrimSpace(c.Username) != "" && strings.TrimSpace(c.Password) != ""
}

func collectPhpFpmProcesses(root string) ([]procInfo, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	infos := make([]procInfo, 0, 64)
	masters := make(map[int]string)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		info, err := readProcessInfo(root, pid)
		if err != nil {
			continue
		}
		if !isPhpFpm(info.Title, info.Exe) {
			continue
		}

		if info.Role == "master" && info.MasterConfig != "" {
			masters[info.PID] = info.MasterConfig
		}
		infos = append(infos, info)
	}

	for i := range infos {
		if infos[i].Role == "master" {
			infos[i].MasterConfig = nonEmpty(infos[i].MasterConfig, "unknown")
			infos[i].MasterPID = infos[i].PID
			continue
		}
		if cfg, ok := masters[infos[i].PPID]; ok {
			infos[i].MasterConfig = cfg
			infos[i].MasterPID = infos[i].PPID
		} else {
			infos[i].MasterConfig = nonEmpty(infos[i].MasterConfig, "unknown")
			infos[i].MasterPID = infos[i].PPID
		}
	}

	return infos, nil
}

func readProcessInfo(root string, pid int) (procInfo, error) {
	base := filepath.Join(root, strconv.Itoa(pid))
	title := readCmdline(filepath.Join(base, "cmdline"))
	comm := readText(filepath.Join(base, "comm"))
	if title == "" {
		title = comm
	}

	exe := readExeBase(filepath.Join(base, "exe"))
	if title == "" && exe != "" {
		title = exe
	}

	st, err := readStat(filepath.Join(base, "stat"))
	if err != nil {
		return procInfo{}, err
	}

	uid, userName := readUID(filepath.Join(base, "status"))
	if userName == "" {
		userName = resolveUser(uid)
	}

	cpuNs, err := readSchedstatNs(filepath.Join(base, "schedstat"))
	if err != nil {
		cpuNs = 0
	}

	role, workerPool, masterConfig := classifyProcess(title)

	return procInfo{
		PID:           pid,
		PPID:          st.PPID,
		UID:           uid,
		User:          userName,
		Title:         title,
		Exe:           exe,
		Role:          role,
		WorkerPool:    workerPool,
		MasterConfig:  masterConfig,
		CPUSeconds:    float64(cpuNs) / 1e9,
		ResidentBytes: uint64(st.RSS) * uint64(os.Getpagesize()),
		VirtualBytes:  st.VSize,
		Threads:       st.NumThreads,
	}, nil
}

type procStat struct {
	PPID       int
	NumThreads int
	VSize      uint64
	RSS        int64
}

func readStat(path string) (procStat, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return procStat{}, err
	}
	s := string(raw)
	end := strings.LastIndexByte(s, ')')
	if end == -1 {
		return procStat{}, fmt.Errorf("unexpected stat format")
	}
	fields := strings.Fields(s[end+2:])
	if len(fields) < 22 {
		return procStat{}, fmt.Errorf("unexpected stat field count: %d", len(fields))
	}

	ppid, _ := strconv.Atoi(fields[1])
	numThreads, _ := strconv.Atoi(fields[17])
	vsize, _ := strconv.ParseUint(fields[20], 10, 64)
	rss, _ := strconv.ParseInt(fields[21], 10, 64)

	return procStat{PPID: ppid, NumThreads: numThreads, VSize: vsize, RSS: rss}, nil
}

func readSchedstatNs(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty schedstat")
	}
	return strconv.ParseUint(fields[0], 10, 64)
}

func readCmdline(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return ""
	}
	raw = bytes.TrimRight(raw, "\x00")
	if len(raw) == 0 {
		return ""
	}
	parts := bytes.Split(raw, []byte{0})
	flattened := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		flattened = append(flattened, string(p))
	}
	return strings.TrimSpace(strings.Join(flattened, " "))
}

func readText(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func readExeBase(path string) string {
	target, err := os.Readlink(path)
	if err != nil {
		return ""
	}
	return filepath.Base(target)
}

func readUID(path string) (int, string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, ""
		}
		uid, _ := strconv.Atoi(fields[1])
		return uid, resolveUser(uid)
	}
	return 0, ""
}

func resolveUser(uid int) string {
	if uid == 0 {
		return "root"
	}
	if v, ok := userCache.Load(uid); ok {
		return v.(string)
	}
	u, err := user.LookupId(strconv.Itoa(uid))
	if err != nil || u == nil || u.Username == "" {
		fallback := strconv.Itoa(uid)
		userCache.Store(uid, fallback)
		return fallback
	}
	userCache.Store(uid, u.Username)
	return u.Username
}

func isPhpFpm(title, exe string) bool {
	title = strings.ToLower(title)
	exe = strings.ToLower(exe)
	return strings.Contains(title, "php-fpm") || strings.Contains(exe, "php-fpm")
}

func classifyProcess(title string) (role, workerPool, masterConfig string) {
	if m := masterTitleRE.FindStringSubmatch(title); len(m) == 2 {
		return "master", "", strings.TrimSpace(m[1])
	}
	if m := workerTitleRE.FindStringSubmatch(title); len(m) == 2 {
		return "worker", strings.TrimSpace(m[1]), ""
	}
	if strings.Contains(strings.ToLower(title), "php-fpm") {
		return "php-fpm", "", ""
	}
	return "unknown", "", ""
}

func collectThreadMetrics(root string, procs []procInfo) ([]threadInfo, error) {
	var out []threadInfo
	for _, p := range procs {
		tasksDir := filepath.Join(root, strconv.Itoa(p.PID), "task")
		entries, err := os.ReadDir(tasksDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			tid, err := strconv.Atoi(entry.Name())
			if err != nil {
				continue
			}
			tinfo, err := readThreadInfo(root, p, tid)
			if err != nil {
				continue
			}
			out = append(out, tinfo)
		}
	}
	return out, nil
}

func readThreadInfo(root string, p procInfo, tid int) (threadInfo, error) {
	base := filepath.Join(root, strconv.Itoa(p.PID), "task", strconv.Itoa(tid))
	title := readText(filepath.Join(base, "comm"))
	if title == "" {
		title = p.Title
	}
	cpuNs, err := readSchedstatNs(filepath.Join(base, "schedstat"))
	if err != nil {
		return threadInfo{}, err
	}
	return threadInfo{
		PID:        p.PID,
		TID:        tid,
		Title:      title,
		CPUSeconds: float64(cpuNs) / 1e9,
	}, nil
}

func writeProcessMetricsHeader(w io.Writer) {
	_, _ = io.WriteString(w, "# HELP php_fpm_process_info Always 1 for discovered php-fpm processes.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_process_info gauge\n")
	_, _ = io.WriteString(w, "# HELP php_fpm_process_cpu_seconds_total Cumulative CPU seconds consumed by php-fpm processes.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_process_cpu_seconds_total counter\n")
	_, _ = io.WriteString(w, "# HELP php_fpm_process_resident_memory_bytes Resident memory size in bytes for php-fpm processes.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_process_resident_memory_bytes gauge\n")
	_, _ = io.WriteString(w, "# HELP php_fpm_process_virtual_memory_bytes Virtual memory size in bytes for php-fpm processes.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_process_virtual_memory_bytes gauge\n")
	_, _ = io.WriteString(w, "# HELP php_fpm_process_threads Number of threads in the php-fpm process.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_process_threads gauge\n")
}

func writeThreadMetricsHeader(w io.Writer) {
	_, _ = io.WriteString(w, "# HELP php_fpm_thread_cpu_seconds_total Cumulative CPU seconds consumed by php-fpm threads.\n")
	_, _ = io.WriteString(w, "# TYPE php_fpm_thread_cpu_seconds_total counter\n")
}

func writeProcessMetrics(w io.Writer, p procInfo) {
	labels := fmt.Sprintf(`pid="%d",ppid="%d",uid="%d",user="%s",role="%s",master_config="%s",worker_pool="%s",process_title="%s"`,
		p.PID, p.PPID, p.UID, escapeLabelValue(p.User), escapeLabelValue(p.Role), escapeLabelValue(p.MasterConfig), escapeLabelValue(p.WorkerPool), escapeLabelValue(p.Title))
	_, _ = fmt.Fprintf(w, "php_fpm_process_info{%s} 1\n", labels)
	_, _ = fmt.Fprintf(w, "php_fpm_process_cpu_seconds_total{%s} %.6f\n", labels, p.CPUSeconds)
	_, _ = fmt.Fprintf(w, "php_fpm_process_resident_memory_bytes{%s} %d\n", labels, p.ResidentBytes)
	_, _ = fmt.Fprintf(w, "php_fpm_process_virtual_memory_bytes{%s} %d\n", labels, p.VirtualBytes)
	_, _ = fmt.Fprintf(w, "php_fpm_process_threads{%s} %d\n", labels, p.Threads)
}

func writeThreadMetric(w io.Writer, t threadInfo) {
	labels := fmt.Sprintf(`pid="%d",tid="%d",thread_title="%s"`, t.PID, t.TID, escapeLabelValue(t.Title))
	_, _ = fmt.Fprintf(w, "php_fpm_thread_cpu_seconds_total{%s} %.6f\n", labels, t.CPUSeconds)
}

func escapeLabelValue(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return v
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
