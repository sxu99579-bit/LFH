package bridge

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskManagerPersistenceAndConcurrentClaimRace(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewTaskManager(dir)
	if err != nil {
		t.Fatalf("NewTaskManager: %v", err)
	}
	defer mgr.Close()

	human := TaskActor{Username: "alice", Role: "user"}
	created, err := mgr.CreateTask(human, CreateTaskInput{
		Title:        "整理 35 张图片",
		Description:  "请扫描并归纳汇总",
		Priority:     "high",
		FolderPrefix: "reports",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Status != TaskStatusPending || created.MaxCommentRounds != DefaultMaxCommentRounds {
		t.Fatalf("unexpected created task: %+v", created)
	}

	// Reload from disk in a fresh TaskManager.
	mgr.Close()
	mgr2, err := NewTaskManager(dir)
	if err != nil {
		t.Fatalf("reload TaskManager: %v", err)
	}
	defer mgr2.Close()

	got, err := mgr2.GetTask(human, created.ID)
	if err != nil || got.Title != created.Title {
		t.Fatalf("GetTask after reload: got=%+v err=%v", got, err)
	}

	// 8 concurrent Agents race to claim the same task -> exactly 1 winner.
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ag := TaskActor{
				Username: "alice",
				Role:     "user",
				IsAgent:  true,
				KeyID:    "agk_" + string(rune('a'+idx)),
				Alias:    "Agent-" + string(rune('A'+idx)),
			}
			if _, claimErr := mgr2.ClaimTask(ag, ClaimTaskInput{TaskID: created.ID}); claimErr == nil {
				wins.Add(1)
			} else if !errors.Is(claimErr, ErrTaskAlreadyClaimed) {
				t.Errorf("unexpected error during claim race: %v", claimErr)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("expected exactly 1 atomic claim winner, got %d", wins.Load())
	}
}

func TestTaskManagerTargetedLockScheduledSplitAndLeaseExpiry(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewTaskManager(dir)
	if err != nil {
		t.Fatalf("NewTaskManager: %v", err)
	}
	defer mgr.Close()

	base := time.Date(2026, 11, 24, 16, 0, 0, 0, time.Local)
	var clockMu sync.Mutex
	curNow := base
	mgr.nowFn = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return curNow
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		curNow = curNow.Add(d)
		clockMu.Unlock()
	}

	admin := TaskActor{Username: "admin", Role: "admin"}
	shiba := TaskActor{Username: "admin", Role: "admin", IsAgent: true, KeyID: "agk_shiba", KeyName: "LAPTOP-antigravity", Alias: "小柴"}
	bluecat := TaskActor{Username: "admin", Role: "admin", IsAgent: true, KeyID: "agk_cat", KeyName: "DESKTOP-cursor", Alias: "蓝猫"}

	// 1. Targeted @shiba task scheduled 10 minutes in the future.
	schedTime := base.Add(10 * time.Minute)
	lockedTask, err := mgr.CreateTask(admin, CreateTaskInput{
		Title:              "@小柴 批量处理扫描件",
		AssignedAgent:      shiba.KeyID,
		AssignedAgentLabel: shiba.Alias,
		ScheduledAt:        &schedTime,
	})
	if err != nil {
		t.Fatalf("CreateTask scheduled: %v", err)
	}
	if lockedTask.Status != TaskStatusScheduled {
		t.Fatalf("expected scheduled, got %s", lockedTask.Status)
	}

	// Cannot claim before scheduled time.
	if _, err := mgr.ClaimTask(shiba, ClaimTaskInput{TaskID: lockedTask.ID}); !errors.Is(err, ErrTaskScheduledInFuture) {
		t.Fatalf("expected ErrTaskScheduledInFuture, got %v", err)
	}

	// Advance clock past schedule & sweep -> becomes pending; wrong agent still rejected.
	advance(11 * time.Minute)
	mgr.RunSweep()
	if _, err := mgr.ClaimTask(bluecat, ClaimTaskInput{TaskID: lockedTask.ID}); !errors.Is(err, ErrTaskAssignedToOther) {
		t.Fatalf("expected ErrTaskAssignedToOther for bluecat, got %v", err)
	}
	if claimed, err := mgr.ClaimTask(shiba, ClaimTaskInput{TaskID: lockedTask.ID, LeaseSeconds: 60}); err != nil || claimed.ClaimedByID != shiba.KeyID {
		t.Fatalf("expected shiba to claim locked task: %+v err=%v", claimed, err)
	}

	// Lease expiry releases back to pending.
	advance(65 * time.Second)
	mgr.RunSweep()
	afterExp, err := mgr.GetTask(admin, lockedTask.ID)
	if err != nil || afterExp.Status != TaskStatusPending || afterExp.ClaimedByID != "" {
		t.Fatalf("expected lease expiry to release task back to pending, got %+v err=%v", afterExp, err)
	}

	// 2. Complex open task -> SplitTask with dependency -> collaborative completion.
	bigTask, err := mgr.CreateTask(admin, CreateTaskInput{
		Title: "多阶段大任务",
	})
	if err != nil {
		t.Fatalf("CreateTask bigTask: %v", err)
	}
	split, err := mgr.SplitTask(shiba, SplitTaskInput{
		TaskID: bigTask.ID,
		Note:   "@蓝猫 我先做 #1，请接 #2 并按约输出 part2.md",
		Subtasks: []SplitSubtaskSpec{
			{Title: "前 20 张 OCR"},
			{Title: "后 15 张 OCR"},
			{Title: "合并周报", DependsOn: []int{1, 2}},
		},
	})
	if err != nil {
		t.Fatalf("SplitTask: %v", err)
	}
	if len(split.Subtasks) != 3 || split.Subtasks[0].Status != SubtaskStatusClaimed || split.Subtasks[0].ClaimedByID != shiba.KeyID {
		t.Fatalf("unexpected split state: %+v", split.Subtasks)
	}

	// Mention check: bluecat has an unread @蓝猫 mention!
	mentions := mgr.ListTasks(bluecat, TaskListFilter{MentionedMe: true})
	if mentions.Summary.MentionedForMe != 1 {
		t.Fatalf("expected MentionedForMe=1 for bluecat, got %d", mentions.Summary.MentionedForMe)
	}

	// Subtask #3 blocked until #1 & #2 are checked.
	if _, err := mgr.ClaimTask(bluecat, ClaimTaskInput{TaskID: bigTask.ID, SubtaskID: "sub_3"}); !errors.Is(err, ErrSubtaskBlockedByDeps) {
		t.Fatalf("expected ErrSubtaskBlockedByDeps for sub_3, got %v", err)
	}

	// Bluecat claims #2, posts reply (clears mention notification!), completes #2.
	if _, err := mgr.ClaimTask(bluecat, ClaimTaskInput{TaskID: bigTask.ID, SubtaskID: "sub_2"}); err != nil {
		t.Fatalf("claim sub_2: %v", err)
	}
	if _, _, err := mgr.PostComment(bluecat, PostCommentInput{TaskID: bigTask.ID, Body: "收到 @小柴 ，我先做 #2"}); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	mentionsAfter := mgr.ListTasks(bluecat, TaskListFilter{MentionedMe: true})
	if mentionsAfter.Summary.MentionedForMe != 0 {
		t.Fatalf("expected MentionedForMe=0 after bluecat replied, got %d", mentionsAfter.Summary.MentionedForMe)
	}

	if _, err := mgr.CompleteTask(shiba, CompleteTaskInput{TaskID: bigTask.ID, SubtaskID: "sub_1", OutputFiles: []string{"part1.md"}}); err != nil {
		t.Fatalf("complete sub_1: %v", err)
	}
	if _, err := mgr.CompleteTask(bluecat, CompleteTaskInput{TaskID: bigTask.ID, SubtaskID: "sub_2", OutputFiles: []string{"part2.md"}}); err != nil {
		t.Fatalf("complete sub_2: %v", err)
	}

	// Now #3 is unlocked!
	if _, err := mgr.ClaimTask(bluecat, ClaimTaskInput{TaskID: bigTask.ID, SubtaskID: "sub_3"}); err != nil {
		t.Fatalf("claim sub_3 after deps: %v", err)
	}
	doneParent, err := mgr.CompleteTask(bluecat, CompleteTaskInput{
		TaskID:        bigTask.ID,
		SubtaskID:     "sub_3",
		OutputFiles:   []string{"weekly.md"},
		ResultSummary: "汇总完成",
	})
	if err != nil {
		t.Fatalf("complete sub_3: %v", err)
	}
	if doneParent.Status != TaskStatusDone || len(doneParent.OutputFiles) != 3 {
		t.Fatalf("expected parent task done with 3 output files, got %+v", doneParent)
	}
}

func TestTaskManagerCommentRoundLimitAutoPause(t *testing.T) {
	mgr, err := NewTaskManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewTaskManager: %v", err)
	}
	defer mgr.Close()

	admin := TaskActor{Username: "admin", Role: "admin"}
	ag := TaskActor{Username: "admin", Role: "admin", IsAgent: true, KeyID: "agk_1", Alias: "小柴"}
	task, err := mgr.CreateTask(admin, CreateTaskInput{
		Title:            "限制讨论轮次",
		MaxCommentRounds: 2,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := mgr.PostComment(ag, PostCommentInput{TaskID: task.ID, Body: "第 1 条"}); err != nil {
		t.Fatalf("round 1: %v", err)
	}
	if _, _, err := mgr.PostComment(ag, PostCommentInput{TaskID: task.ID, Body: "第 2 条"}); err != nil {
		t.Fatalf("round 2: %v", err)
	}
	if _, _, err := mgr.PostComment(ag, PostCommentInput{TaskID: task.ID, Body: "第 3 条"}); !errors.Is(err, ErrTaskCommentLimitReached) {
		t.Fatalf("expected ErrTaskCommentLimitReached on round 3, got %v", err)
	}
	got, _ := mgr.GetTask(admin, task.ID)
	if got.Status != TaskStatusPaused {
		t.Fatalf("expected paused after hitting agent round limit, got %s", got.Status)
	}
	// Human can still comment even when limit reached.
	if _, _, err := mgr.PostComment(admin, PostCommentInput{TaskID: task.ID, Body: "人工拍板继续"}); err != nil {
		t.Fatalf("human comment after pause: %v", err)
	}
}

func TestTaskManagerSnapshotExportImport(t *testing.T) {
	dirSrc := t.TempDir()
	dirDst := t.TempDir()

	src, err := NewTaskManager(dirSrc)
	if err != nil {
		t.Fatalf("src NewTaskManager: %v", err)
	}
	defer src.Close()
	dst, err := NewTaskManager(dirDst)
	if err != nil {
		t.Fatalf("dst NewTaskManager: %v", err)
	}
	defer dst.Close()

	admin := TaskActor{Username: "admin", Role: "admin"}
	allowSplit := true
	created, err := src.CreateTask(admin, CreateTaskInput{
		Title:       "HA Failover Test Task",
		Description: "verify task board hot standby",
		Priority:    "high",
		AllowSplit:  &allowSplit,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	snap, err := src.ExportSnapshot()
	if err != nil {
		t.Fatalf("ExportSnapshot: %v", err)
	}
	h1 := src.SnapshotHash()
	if h1 == "" {
		t.Fatal("expected non-empty snapshot hash")
	}

	if err := dst.ImportSnapshot([]byte("{not-json")); err == nil {
		t.Fatal("expected invalid JSON snapshot to fail")
	}
	if err := dst.ImportSnapshot(snap); err != nil {
		t.Fatalf("ImportSnapshot: %v", err)
	}
	if h2 := dst.SnapshotHash(); h2 != h1 {
		t.Fatalf("expected matching snapshot hash after import, src=%s dst=%s", h1, h2)
	}
	got, err := dst.GetTask(admin, created.ID)
	if err != nil || got.Title != "HA Failover Test Task" {
		t.Fatalf("expected synced task on dst, err=%v got=%+v", err, got)
	}
}
