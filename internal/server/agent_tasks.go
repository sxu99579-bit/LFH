package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"lfh/internal/auth"
	"lfh/internal/bridge"
)

var taskTextMentionRe = regexp.MustCompile(`@([^\s@,:;!?()（）【】<>]+)`)

func taskActorFromPrincipal(p auth.Principal) bridge.TaskActor {
	actor := bridge.TaskActor{
		Username: p.User.Username,
		Role:     p.User.Role,
		IsAgent:  p.IsAgent,
	}
	if p.IsAgent && p.AgentKey != nil {
		actor.KeyID = p.AgentKey.ID
		actor.KeyName = p.AgentKey.Name
		actor.Alias = p.AgentKey.DisplayAlias
	}
	return actor
}

func writeTaskErr(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case errors.Is(err, bridge.ErrTaskNotFound), errors.Is(err, bridge.ErrSubtaskNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": msg})
	case errors.Is(err, bridge.ErrTaskForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": msg})
	case errors.Is(err, bridge.ErrTaskInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
	case errors.Is(err, bridge.ErrTaskAssignedToOther),
		errors.Is(err, bridge.ErrTaskAlreadyClaimed),
		errors.Is(err, bridge.ErrTaskNotClaimable),
		errors.Is(err, bridge.ErrTaskScheduledInFuture),
		errors.Is(err, bridge.ErrSubtaskBlockedByDeps),
		errors.Is(err, bridge.ErrTaskLeaseNotHeld),
		errors.Is(err, bridge.ErrTaskSplitNotAllowed),
		errors.Is(err, bridge.ErrTaskAlreadySplit),
		errors.Is(err, bridge.ErrTaskCommentLimitReached):
		writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
	}
}

func (s *Server) resolveExplicitAgentTarget(rawRef string) (keyID, label string, err error) {
	clean := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rawRef), "@"))
	if clean == "" {
		return "", "", nil
	}
	pub, ok := s.auth.ResolveAgentKeyRef(clean)
	if !ok {
		return "", "", errors.New("指定的 Agent 不存在: " + clean)
	}
	lbl := pub.DisplayAlias
	if lbl == "" {
		lbl = pub.Name
	}
	return pub.ID, lbl, nil
}

func (s *Server) detectAssignedAgentFromText(title, desc string) (keyID, label string) {
	joined := title + " " + desc
	matches := taskTextMentionRe.FindAllStringSubmatch(joined, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		if pub, ok := s.auth.ResolveAgentKeyRef(m[1]); ok {
			lbl := pub.DisplayAlias
			if lbl == "" {
				lbl = pub.Name
			}
			return pub.ID, lbl
		}
	}
	return "", ""
}

func (s *Server) handleListAgentTasks(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(strings.TrimSpace(q.Get("limit")))
	filter := bridge.TaskListFilter{
		Status:        strings.TrimSpace(q.Get("status")),
		AssignedAgent: strings.TrimSpace(q.Get("assigned_agent")),
		FolderPrefix:  strings.TrimSpace(q.Get("folder_prefix")),
		ClaimableOnly: strings.EqualFold(strings.TrimSpace(q.Get("claimable_only")), "true"),
		Limit:         limit,
	}
	if filter.AssignedAgent != "" {
		if pub, ok := s.auth.ResolveAgentKeyRef(filter.AssignedAgent); ok {
			filter.AssignedAgent = pub.ID
		}
	}
	res := s.getTasks().ListTasks(taskActorFromPrincipal(auth.Principal{User: u}), filter)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"summary":     res.Summary,
		"tasks":       res.Tasks,
		"server_time": time.Now(),
	})
}

func (s *Server) handleCreateAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	var in bridge.CreateTaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if strings.TrimSpace(in.AssignedAgent) != "" {
		id, lbl, err := s.resolveExplicitAgentTarget(in.AssignedAgent)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		in.AssignedAgent = id
		if in.AssignedAgentLabel == "" {
			in.AssignedAgentLabel = lbl
		}
	} else {
		id, lbl := s.detectAssignedAgentFromText(in.Title, in.Description)
		if id != "" {
			in.AssignedAgent = id
			if in.AssignedAgentLabel == "" {
				in.AssignedAgentLabel = lbl
			}
		}
	}

	task, err := s.getTasks().CreateTask(taskActorFromPrincipal(auth.Principal{User: u}), in)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_task_create", task.Title, "ok", "id="+task.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Server) handleGetAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	task, err := s.getTasks().GetTask(taskActorFromPrincipal(auth.Principal{User: u}), id)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Server) handleUpdateAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	var in bridge.UpdateTaskInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	if in.AssignedAgent != nil {
		clean := strings.TrimSpace(*in.AssignedAgent)
		if clean == "" {
			empty := ""
			in.AssignedAgent = &empty
			in.AssignedAgentLabel = &empty
		} else {
			keyID, lbl, err := s.resolveExplicitAgentTarget(clean)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			in.AssignedAgent = &keyID
			if in.AssignedAgentLabel == nil {
				in.AssignedAgentLabel = &lbl
			}
		}
	}

	task, err := s.getTasks().UpdateTask(taskActorFromPrincipal(auth.Principal{User: u}), id, in)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_task_update", task.Title, "ok", "id="+task.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Server) handleDeleteAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	task, err := s.getTasks().DeleteTask(taskActorFromPrincipal(auth.Principal{User: u}), id)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_task_delete", task.Title, "ok", "id="+task.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleActivateAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	task, err := s.getTasks().ActivateTask(taskActorFromPrincipal(auth.Principal{User: u}), id)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_task_activate", task.Title, "ok", "id="+task.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Server) handleReopenAgentTask(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	task, err := s.getTasks().ReopenTask(taskActorFromPrincipal(auth.Principal{User: u}), id)
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	s.logAudit(r, u.Username, "agent_task_reopen", task.Title, "ok", "id="+task.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"task": task,
	})
}

func (s *Server) handlePostAgentTaskComment(w http.ResponseWriter, r *http.Request, u auth.PublicUser) {
	id := strings.TrimSpace(r.PathValue("id"))
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	task, cmt, err := s.getTasks().PostComment(taskActorFromPrincipal(auth.Principal{User: u}), bridge.PostCommentInput{
		TaskID: id,
		Body:   req.Body,
	})
	if err != nil {
		writeTaskErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok":      true,
		"task":    task,
		"comment": cmt,
	})
}

func (s *Server) collectPollTasks(actor bridge.TaskActor) (claimableCount, mentionedCount int, tasks []bridge.AgentTask) {
	tm := s.getTasks()
	claimRes := tm.ListTasks(actor, bridge.TaskListFilter{ClaimableOnly: true, Limit: 50})
	mentionRes := tm.ListTasks(actor, bridge.TaskListFilter{MentionedMe: true, Limit: 50})

	seen := make(map[string]bool, len(claimRes.Tasks)+len(mentionRes.Tasks))
	tasks = make([]bridge.AgentTask, 0, len(claimRes.Tasks)+len(mentionRes.Tasks))
	for _, t := range claimRes.Tasks {
		seen[t.ID] = true
		tasks = append(tasks, t)
	}
	for _, t := range mentionRes.Tasks {
		if !seen[t.ID] {
			seen[t.ID] = true
			tasks = append(tasks, t)
		}
	}
	return claimRes.Summary.ClaimableForMe, mentionRes.Summary.MentionedForMe, tasks
}

func (s *Server) handlePollAgentTasks(w http.ResponseWriter, r *http.Request, p auth.Principal) {
	waitSec := 25
	if rawWait := strings.TrimSpace(r.URL.Query().Get("wait_sec")); rawWait != "" {
		if parsed, err := strconv.Atoi(rawWait); err == nil {
			waitSec = parsed
		}
	}
	if waitSec < 0 {
		waitSec = 0
	}
	if waitSec > 30 {
		waitSec = 30
	}

	actor := taskActorFromPrincipal(p)
	tm := s.getTasks()
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
	var doneCh <-chan struct{}
	if s.events != nil {
		doneCh = s.events.Done()
	}

	for {
		// Subscribe before inspecting snapshot so concurrent mutations are never missed.
		waitCh := tm.WaitChangeCh()
		claimable, mentioned, tasks := s.collectPollTasks(actor)
		if claimable > 0 || mentioned > 0 || waitSec == 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":              true,
				"has_work":        claimable > 0 || mentioned > 0,
				"timed_out":       false,
				"claimable_count": claimable,
				"mentioned_count": mentioned,
				"tasks":           tasks,
				"server_time":     time.Now(),
			})
			return
		}

		rem := time.Until(deadline)
		if rem <= 0 {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":              true,
				"has_work":        false,
				"timed_out":       true,
				"claimable_count": 0,
				"mentioned_count": 0,
				"tasks":           []bridge.AgentTask{},
				"server_time":     time.Now(),
			})
			return
		}

		wakeDur := rem
		if nextDue, hasDue := tm.NextWakeAt(actor); hasDue {
			if d := time.Until(nextDue); d >= 0 && d < wakeDur {
				wakeDur = d
				if wakeDur < 10*time.Millisecond {
					wakeDur = 10 * time.Millisecond
				}
			}
		}

		timer := time.NewTimer(wakeDur)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-doneCh:
			timer.Stop()
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":              true,
				"has_work":        false,
				"timed_out":       true,
				"claimable_count": 0,
				"mentioned_count": 0,
				"tasks":           []bridge.AgentTask{},
				"server_time":     time.Now(),
			})
			return
		case <-waitCh:
			timer.Stop()
		case <-timer.C:
			tm.RunSweep()
		}
	}
}
