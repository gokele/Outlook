package httpapi

// 文件导入。
//
// 与 JSON 导入的区别只有一个，但很关键：文本不进请求体，而是以 multipart
// 的方式流式读取。十万行账号的文本有十几兆，JSON 那条路要么撞上请求体上限，
// 要么逼着调用方把文件拆开 —— 而"请自行分批"从来不是解决办法。
//
// 这里对文件大小不设上限：内存占用由 importer.Stream 的分段处理决定，
// 与文件多大无关。

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kele/outlook-console/internal/importer"
)

// handleImportFile 从上传的文件导入账号。
// POST /api/admin/import/file  (multipart/form-data)
func (s *Server) handleImportFile(w http.ResponseWriter, r *http.Request) {
	// 只解析表单字段，文件部分留给流式读取。
	// 32KB 的内存上限只作用于非文件字段，文件不会被读进内存或落盘。
	if err := r.ParseMultipartForm(32 << 10); err != nil {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "解析上传内容失败: "+err.Error()), s.log)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, r, newAPIError(400, "BAD_REQUEST", "缺少上传文件"), s.log)
		return
	}
	defer file.Close()

	req := importer.Request{
		Separator:   r.FormValue("separator"),
		Tenant:      s.cfg.Tenant,
		OnDuplicate: importer.OnDuplicate(r.FormValue("on_duplicate")),
		DryRun:      r.FormValue("dry_run") == "true",
	}
	// category_id 来自下拉框，可能是空串、数字或数字字符串，三种都要认。
	if v := strings.TrimSpace(r.FormValue("category_id")); v != "" {
		id, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			writeError(w, r, newAPIError(400, "BAD_REQUEST", "category_id 不是合法的数字"), s.log)
			return
		}
		req.CategoryID = &id
	}
	// tags 以逗号分隔传来，空项丢弃。
	for _, t := range strings.Split(r.FormValue("tags"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			req.Tags = append(req.Tags, t)
		}
	}

	res, err := s.imp.Stream(r.Context(), file, req)
	if err != nil {
		// 已经处理掉的部分照样回带：几十万行跑到一半失败时，
		// 只回一句错误等于让人完全不知道哪些已经入库了。
		writeError(w, r, &APIError{
			Status: 400, Code: "IMPORT_FAILED", Msg: err.Error(),
			Extra: map[string]any{"partial": res},
		}, s.log)
		return
	}
	s.log.Info("文件导入完成", "file", header.Filename, "size", header.Size,
		"total", res.Total, "added", res.Added, "dry_run", req.DryRun)
	writeJSON(w, r, res)
}

// importTimeout 是文件导入的超时。
//
// 全局中间件是 180 秒，那是按长轮询定的。几十万行的导入远不止这个量级，
// 用全局值会让一次本来能成功的导入在写到一半时被掐断。
const importTimeout = 30 * time.Minute
