package storage

import (
	"sync"

	"github.com/google/btree"
)

type ZSetEntry struct {
	Score  float64
	Member string
}

func (a ZSetEntry) Less(b btree.Item) bool {
	other := b.(ZSetEntry)
	
	if a.Score != other.Score {
		return a.Score < other.Score
	}
	
	return a.Member < other.Member
}

type ZSet struct {
	tree   *btree.BTree
	byName map[string]ZSetEntry
	mutex  sync.RWMutex
}

func NewZSet() *ZSet {
	return &ZSet{
		tree:   btree.New(2),
		byName: make(map[string]ZSetEntry),
	}
}

func (z *ZSet) Add(score float64, member string) bool {
	z.mutex.Lock()
	defer z.mutex.Unlock()

	if oldEntry, exists := z.byName[member]; exists {
		z.tree.Delete(oldEntry)
	}

	entry := ZSetEntry{Score: score, Member: member}
	z.tree.ReplaceOrInsert(entry)
	z.byName[member] = entry

	return true
}

func (z *ZSet) Remove(member string) bool {
	z.mutex.Lock()
	defer z.mutex.Unlock()

	entry, exists := z.byName[member]
	if !exists {
		return false
	}

	z.tree.Delete(entry)
	delete(z.byName, member)

	return true
}

func (z *ZSet) GetScore(member string) (float64, bool) {
	z.mutex.RLock()
	defer z.mutex.RUnlock()

	entry, exists := z.byName[member]
	if !exists {
		return 0, false
	}

	return entry.Score, true
}

func (z *ZSet) Size() int {
	z.mutex.RLock()
	defer z.mutex.RUnlock()

	return len(z.byName)
}

func (z *ZSet) Range(min, max float64) []ZSetEntry {
	z.mutex.RLock()
	defer z.mutex.RUnlock()

	var result []ZSetEntry
	
	pivot := ZSetEntry{Score: min, Member: ""}
	
	z.tree.AscendGreaterOrEqual(pivot, func(item btree.Item) bool {
		entry := item.(ZSetEntry)
		
		if entry.Score > max {
			return false
		}
		
		result = append(result, entry)
		return true
	})
	
	return result
}
