package sessioncontroller

import (
	"reflect"
	"testing"
)

func generationWriter() *Writer {
	return &Writer{queues: make(map[string][]queuedEntry)}
}

func addGenerationEntry(writer *Writer, path, text string) {
	writer.appendQueuedLocked(path, queuedEntry{entry: plainMessage(text, text)})
}

func takeGeneration(t *testing.T, writer *Writer, job *pendingDrain, path, text string) {
	t.Helper()
	got, entries, exists := job.nextQueueGenerationLocked(writer)
	if !exists || got != path || len(entries) != 1 || entries[0].entry.String("uuid") != text {
		t.Fatalf("entry got=%q entries=%v exists=%v; want path=%q uuid=%q", got, entries, exists, path, text)
	}
	writer.queues[got] = nil
}

func requireIteratorDone(t *testing.T, writer *Writer, job *pendingDrain) {
	t.Helper()
	if path, entries, exists := job.nextQueueGenerationLocked(writer); exists {
		t.Fatalf("iterator revisited path=%q entries=%v", path, entries)
	}
}

func TestQueueGenerationSameEntryLatePushWaitsForNextDrain(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "same", "first")
	generation := writer.queueGenerations["same"]
	first := &pendingDrain{}
	takeGeneration(t, writer, first, "same", "first")
	addGenerationEntry(writer, "same", "late")
	if writer.queueGenerations["same"] != generation {
		t.Fatal("append replaced an existing Map entry")
	}
	requireIteratorDone(t, writer, first)
	writer.deleteEmptyQueuesLocked()
	if len(writer.queues["same"]) != 1 || writer.queueGenerations["same"] != generation {
		t.Fatal("successful drain cleanup consumed late same-generation queue")
	}
	takeGeneration(t, writer, &pendingDrain{}, "same", "late")
}

func TestQueueGenerationDeleteReinsertSameKeyIsVisitedAgain(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "same", "first")
	old := writer.queueGenerations["same"]
	job := &pendingDrain{}
	takeGeneration(t, writer, job, "same", "first")
	writer.deleteQueueLocked("same")
	addGenerationEntry(writer, "same", "reinserted")
	if writer.queueGenerations["same"] == old {
		t.Fatal("delete/reinsert reused an old Map generation")
	}
	takeGeneration(t, writer, job, "same", "reinserted")
	requireIteratorDone(t, writer, job)
}

func TestQueueGenerationNewKeyAfterAdmissionRemainsVisibleInOrder(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "a", "first")
	job := &pendingDrain{}
	takeGeneration(t, writer, job, "a", "first")
	addGenerationEntry(writer, "c", "third-key")
	addGenerationEntry(writer, "b", "second-key")
	takeGeneration(t, writer, job, "c", "third-key")
	takeGeneration(t, writer, job, "b", "second-key")
	requireIteratorDone(t, writer, job)
}

func TestQueueGenerationDeletedUnvisitedEntryIsSkippedAndReinsertMovesTail(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "a", "first")
	addGenerationEntry(writer, "b", "deleted")
	addGenerationEntry(writer, "c", "third")
	job := &pendingDrain{}
	takeGeneration(t, writer, job, "a", "first")
	writer.deleteQueueLocked("b")
	addGenerationEntry(writer, "b", "reinserted")
	takeGeneration(t, writer, job, "c", "third")
	takeGeneration(t, writer, job, "b", "reinserted")
	requireIteratorDone(t, writer, job)
}

func TestQueueGenerationVisitedEmptyEntryIsNotDeletedBeforeEnd(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "empty", "discarded")
	writer.clearQueueLocked("empty")
	addGenerationEntry(writer, "active", "first")
	job := &pendingDrain{}
	path, entries, exists := job.nextQueueGenerationLocked(writer)
	if !exists || path != "empty" || len(entries) != 0 {
		t.Fatal("empty Map entry was not visited before active entry")
	}
	takeGeneration(t, writer, job, "active", "first")
	old := writer.queueGenerations["empty"]
	addGenerationEntry(writer, "empty", "late")
	requireIteratorDone(t, writer, job)
	writer.deleteEmptyQueuesLocked()
	if writer.queueGenerations["empty"] != old || len(writer.queues["empty"]) != 1 {
		t.Fatal("visited empty entry became a new generation or lost its late enqueue")
	}
}

func TestQueueGenerationConcurrentDrainCursorsRemainIndependent(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "a", "first")
	outer, inner := &pendingDrain{}, &pendingDrain{}
	takeGeneration(t, writer, outer, "a", "first")
	if path, entries, exists := inner.nextQueueGenerationLocked(writer); !exists || path != "a" || len(entries) != 0 {
		t.Fatal("second drain did not visit the same empty generation")
	}
	// A separate successful drain's cleanup really deletes the empty entry.
	writer.deleteEmptyQueuesLocked()
	addGenerationEntry(writer, "a", "reinserted")
	takeGeneration(t, writer, inner, "a", "reinserted")
	if path, entries, exists := outer.nextQueueGenerationLocked(writer); !exists || path != "a" || len(entries) != 0 {
		t.Fatal("outer iterator missed the new generation already spliced by inner drain")
	}
	requireIteratorDone(t, writer, outer)
	requireIteratorDone(t, writer, inner)
}

func TestQueueGenerationCompactionWaitsForEveryAdmittedDrain(t *testing.T) {
	writer := generationWriter()
	addGenerationEntry(writer, "a", "old")
	old := writer.queueFirst
	first, second := &pendingDrain{}, &pendingDrain{}
	takeGeneration(t, writer, first, "a", "old")
	writer.deleteQueueLocked("a")
	addGenerationEntry(writer, "a", "new")
	pending := &PendingWriter{writer: writer, drains: []*pendingDrain{first, second}}
	pending.removeDrainLocked(second)
	if writer.queueFirst != old || old.next != writer.queueGenerations["a"] {
		t.Fatal("compacted a tombstone still reachable by an admitted iterator")
	}
	takeGeneration(t, writer, first, "a", "new")
	pending.removeDrainLocked(first)
	if writer.queueFirst != writer.queueGenerations["a"] || writer.queueFirst == old || writer.queueLast != writer.queueFirst || writer.queueLast.next != nil {
		t.Fatal("last drain did not compact tombstones while retaining live entry identity")
	}
}

func TestQueueGenerationClearMissingDoesNotCreateMapEntryAndCleanupKeepsLate(t *testing.T) {
	writer := generationWriter()
	writer.clearQueueLocked("missing")
	if len(writer.queues) != 0 || writer.queueFirst != nil {
		t.Fatal("record discard created a missing Map entry")
	}
	addGenerationEntry(writer, "empty", "remove")
	writer.clearQueueLocked("empty")
	addGenerationEntry(writer, "late", "keep")
	writer.deleteEmptyQueuesLocked()
	if !reflect.DeepEqual([]string{writer.queueFirst.path, writer.queueLast.path}, []string{"empty", "late"}) || len(writer.queues) != 1 || len(writer.queues["late"]) != 1 {
		t.Fatal("cleanup removed nonempty entry or rewrote iterator topology")
	}
}
