package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type cache struct {
	mu      sync.Mutex
	data    []byte
	updated time.Time
}

func (c *cache) get(root, home string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.updated) >= 1500*time.Millisecond {
		c.data, _ = json.Marshal(observe(root, home))
		c.updated = time.Now()
	}
	return c.data
}

func main() {
	home, _ := os.UserHomeDir()
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	addr := flag.String("addr", "127.0.0.1:4399", "HTTP address (loopback only)")
	analysisCwd := flag.String("analysis-workspace", ".", "Workbench directory that owns analysis chats")
	workspace := flag.String("workspace", ".", "Root directory to monitor")
	web := flag.String("web", "dist", "Built web files")
	data := flag.String("data", ".data", "Task database directory")
	codexBinary := flag.String("codex-bin", "codex", "Codex CLI executable for planning and development sessions")
	flag.StringVar(&codexHome, "codex-home", codexHome, "Codex data directory")
	flag.Parse()

	host, _, err := net.SplitHostPort(*addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("-addr must use a loopback IP and port")
	}
	root, err := filepath.Abs(*workspace)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		log.Fatal("工作区目录不可用")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		log.Fatal("-workspace 必须指向目录")
	}
	if err := os.MkdirAll(*data, 0700); err != nil {
		log.Fatal("无法创建任务数据目录")
	}
	db, err := sql.Open("sqlite3", filepath.Join(*data, "tasks.sqlite"))
	if err != nil {
		log.Fatal("无法打开任务数据库")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS statuses(id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		log.Fatal("无法初始化任务数据库")
	}

	projects, err := newRegistry(db, root)
	if err != nil {
		log.Fatal("无法初始化项目列表")
	}
	planner, err := newProjectPlanner(db, projects, *codexBinary)
	if err != nil {
		log.Fatal("无法初始化项目档案与会话记录")
	}

	planner.analysisCwd, err = filepath.Abs(*analysisCwd)
	if err == nil {
		planner.analysisCwd, err = filepath.EvalSymlinks(planner.analysisCwd)
	}
	if err != nil {
		log.Fatal("无法解析分析会话工作目录")
	}
	if info, statErr := os.Stat(planner.analysisCwd); statErr != nil || !info.IsDir() {
		log.Fatal("分析会话工作目录必须为工作台目录")
	}
	planner.skillRoot, err = filepath.Abs(filepath.Join(*data, "analysis-skills"))
	if err != nil {
		log.Fatal("无法解析分析技能目录")
	}

	files, err := filepath.Abs(*web)
	if err != nil {
		log.Fatal("无法解析网页目录")
	}
	mux := http.NewServeMux()
	planner.register(mux)
	mux.HandleFunc("GET /api/projects", projects.list)
	mux.HandleFunc("POST /api/projects", projects.add)
	mux.HandleFunc("GET /api/projects/{id}/overview", projects.overview)
	mux.HandleFunc("GET /api/projects/{id}/app-ui-image/{image}", projects.appUIImage)
	mux.HandleFunc("GET /api/projects/{id}/app-code/{code}", projects.appCode)
	mux.HandleFunc("GET /api/projects/{id}/demos", projects.demos)
	mux.HandleFunc("GET /api/projects/{id}/demo-file/{file...}", projects.demoAsset)
	mux.HandleFunc("GET /api/projects/{id}/preview", projects.preview)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/live", func(w http.ResponseWriter, r *http.Request) {
		project, err := projects.project(r.URL.Query().Get("project"))
		if err != nil {
			http.Error(w, "项目不存在", 404)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(planner.live(project, projects.monitor(project.ID).get(project.Root, codexHome)))
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		project, err := projects.project(r.URL.Query().Get("project"))
		if err != nil {
			http.Error(w, "项目不存在", 404)
			return
		}
		state := projects.monitor(project.ID)
		w.Header().Set("Content-Type", "text/event-stream")
		controller := http.NewResponseController(w)
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			controller.SetWriteDeadline(time.Now().Add(4 * time.Second))
			if _, err := fmt.Fprintf(w, "data: %s\n\n", planner.live(project, state.get(project.Root, codexHome))); err != nil || controller.Flush() != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
			}
		}
	})
	mux.HandleFunc("GET /api/task-states", func(w http.ResponseWriter, r *http.Request) {
		rows, err := db.QueryContext(r.Context(), `SELECT id, status FROM statuses`)
		if err != nil {
			http.Error(w, "无法读取任务状态", 500)
			return
		}
		defer rows.Close()
		values := map[string]string{}
		for rows.Next() {
			var id, status string
			if err := rows.Scan(&id, &status); err != nil {
				http.Error(w, "无法读取任务状态", 500)
				return
			}
			values[id] = status
		}
		if rows.Err() != nil {
			http.Error(w, "无法读取任务状态", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(values)
	})
	mux.HandleFunc("PUT /api/task-states", func(w http.ResponseWriter, r *http.Request) {
		var update struct{ ID, Status string }
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if dec.Decode(&update) != nil || dec.Decode(new(any)) != io.EOF || !taskID.MatchString(update.ID) || !taskStatuses[update.Status] {
			http.Error(w, "任务 ID 或状态无效", 400)
			return
		}
		if _, err := db.ExecContext(r.Context(), `INSERT INTO statuses VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET status=excluded.status`, update.ID, update.Status); err != nil {
			http.Error(w, "无法保存任务状态", 500)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write([]byte(`{"saved":true}`))
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(files, "index.html"))
	})
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir(filepath.Join(files, "assets")))))

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := &http.Server{BaseContext: func(net.Listener) context.Context { return stopCtx }, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowedHost := r.Host == net.JoinHostPort(host, port) || r.Host == net.JoinHostPort("localhost", port)
		origin := r.Header.Get("Origin")
		if !allowedHost || (origin != "" && origin != "http://"+r.Host) {
			http.Error(w, "仅允许本机同源访问", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})}
	stopped := make(chan struct{})
	go func() {
		<-stopCtx.Done()
		planner.shutdown()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if server.Shutdown(ctx) != nil {
			_ = server.Close()
		}
		close(stopped)
	}()
	fmt.Printf("项目图谱 http://%s/\n", listener.Addr())
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		log.Print(err)
		stop()
	}
	<-stopped
}

var taskID = regexp.MustCompile(`^[a-zA-Z0-9:_-]{1,96}$`)
var taskStatuses = map[string]bool{"todo": true, "doing": true, "review": true, "blocked": true, "done": true}
