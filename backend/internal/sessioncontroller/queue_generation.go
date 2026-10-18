package sessioncontroller

// transcriptQueueGeneration is one insertion into Source's writeQueues Map.
// Deleting a key leaves its old node available to admitted iterators; setting a
// deleted key creates a new tail node. The queue slice itself stays in queues.
// All access is protected by Writer.mu on the existing Owner path.
type transcriptQueueGeneration struct {
	path string
	next *transcriptQueueGeneration
}

func (writer *Writer) appendQueuedLocked(path string, entry queuedEntry) {
	if writer.queueGenerations == nil {
		writer.queueGenerations = make(map[string]*transcriptQueueGeneration)
	}
	if _, exists := writer.queues[path]; !exists || writer.queueGenerations[path] == nil {
		generation := &transcriptQueueGeneration{path: path}
		if writer.queueLast == nil {
			writer.queueFirst = generation
		} else {
			writer.queueLast.next = generation
		}
		writer.queueLast = generation
		writer.queueGenerations[path] = generation
	}
	writer.queues[path] = append(writer.queues[path], entry)
}

// clearQueueLocked is queue.splice(0), not Map.delete/Map.set. Source does not
// create a missing entry during discard-before-record.
func (writer *Writer) clearQueueLocked(path string) {
	if _, exists := writer.queues[path]; exists {
		writer.queues[path] = nil
	}
}

func (writer *Writer) deleteQueueLocked(path string) {
	delete(writer.queues, path)
	delete(writer.queueGenerations, path)
}

func (writer *Writer) deleteEmptyQueuesLocked() {
	for path, entries := range writer.queues {
		if len(entries) == 0 {
			writer.deleteQueueLocked(path)
		}
	}
}

func (job *pendingDrain) nextQueueGenerationLocked(writer *Writer) (string, []queuedEntry, bool) {
	var next *transcriptQueueGeneration
	if !job.queueStarted {
		job.queueStarted = true
		next = writer.queueFirst
	} else if job.queueCursor != nil {
		next = job.queueCursor.next
	}
	for next != nil {
		job.queueCursor = next
		if writer.queueGenerations[next.path] == next {
			return next.path, writer.queues[next.path], true
		}
		next = next.next
	}
	return "", nil, false
}

// No admitted iterator may retain a node when this is called. Compaction is
// administrative storage only: preserve live insertion order and generation.
func (writer *Writer) compactQueueGenerationsLocked() {
	var first, last *transcriptQueueGeneration
	for current := writer.queueFirst; current != nil; {
		next := current.next
		current.next = nil
		if writer.queueGenerations[current.path] == current {
			if last == nil {
				first = current
			} else {
				last.next = current
			}
			last = current
		}
		current = next
	}
	writer.queueFirst, writer.queueLast = first, last
}
