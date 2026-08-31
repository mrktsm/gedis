package store

import "math"

type ZAddCondition uint8

const (
	ZAddAlways ZAddCondition = iota
	ZAddIfAbsent
	ZAddIfPresent
)

type ZAddComparison uint8

const (
	ZAddAnyScore ZAddComparison = iota
	ZAddIfGreater
	ZAddIfLess
)

type ZAddOptions struct {
	Condition  ZAddCondition
	Comparison ZAddComparison
	Increment  bool
}

type ZUpdate struct {
	Member string
	Score  float64
}

type ZAddResult struct {
	Added   int64
	Updated int64
	Score   float64
	Applied bool
}

type ZItem struct {
	Member string
	Score  float64
}

type sortedSet struct {
	scores map[string]float64
	index  *indexedSkipList
}

func newSortedSet() *sortedSet {
	return &sortedSet{
		scores: make(map[string]float64),
		index:  newIndexedSkipList(),
	}
}

func (k *Keyspace) ZAdd(key string, updates []ZUpdate, options ZAddOptions) (ZAddResult, error) {
	if options.Increment && len(updates) != 1 {
		return ZAddResult{}, ErrInvalidArguments
	}
	for _, update := range updates {
		if math.IsNaN(update.Score) {
			return ZAddResult{}, ErrNotFloat
		}
	}

	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if exists && current.kind != KindSortedSet {
		return ZAddResult{}, ErrWrongType
	}

	set := newSortedSet()
	if exists {
		set = current.sortedSet
	}
	result := ZAddResult{}
	for _, update := range updates {
		oldScore, memberExists := set.scores[update.Member]
		newScore := update.Score
		if options.Increment && memberExists {
			newScore = oldScore + update.Score
			if math.IsNaN(newScore) {
				return ZAddResult{}, ErrNotFloat
			}
		}

		if options.Condition == ZAddIfAbsent && memberExists {
			continue
		}
		if options.Condition == ZAddIfPresent && !memberExists {
			continue
		}
		if memberExists && options.Comparison == ZAddIfGreater && newScore <= oldScore {
			continue
		}
		if memberExists && options.Comparison == ZAddIfLess && newScore >= oldScore {
			continue
		}

		result.Applied = true
		result.Score = newScore
		if !memberExists {
			set.scores[update.Member] = newScore
			set.index.insert(newScore, update.Member)
			result.Added++
			continue
		}
		if newScore == oldScore {
			continue
		}
		set.index.delete(oldScore, update.Member)
		set.index.insert(newScore, update.Member)
		set.scores[update.Member] = newScore
		result.Updated++
	}

	if result.Added > 0 || result.Updated > 0 {
		current.kind = KindSortedSet
		current.sortedSet = set
		current.stringData = nil
		k.setEntryLocked(key, current)
	}
	return result, nil
}

func (k *Keyspace) ZIncrBy(key, member string, increment float64) (float64, bool, error) {
	result, err := k.ZAdd(key, []ZUpdate{{Member: member, Score: increment}}, ZAddOptions{Increment: true})
	if err != nil {
		return 0, false, err
	}
	return result.Score, result.Applied, nil
}

func (k *Keyspace) ZRem(key string, members ...string) (int64, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return 0, nil
	}
	if current.kind != KindSortedSet {
		return 0, ErrWrongType
	}

	var removed int64
	for _, member := range members {
		score, exists := current.sortedSet.scores[member]
		if !exists {
			continue
		}
		current.sortedSet.index.delete(score, member)
		delete(current.sortedSet.scores, member)
		removed++
	}
	if removed == 0 {
		return 0, nil
	}
	if len(current.sortedSet.scores) == 0 {
		delete(k.entries, key)
	} else {
		k.setEntryLocked(key, current)
	}
	return removed, nil
}

func (k *Keyspace) ZScore(key, member string) (float64, bool, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return 0, false, nil
	}
	if current.kind != KindSortedSet {
		return 0, false, ErrWrongType
	}
	score, exists := current.sortedSet.scores[member]
	return score, exists, nil
}

func (k *Keyspace) ZCard(key string) (int64, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return 0, nil
	}
	if current.kind != KindSortedSet {
		return 0, ErrWrongType
	}
	return int64(len(current.sortedSet.scores)), nil
}

func (k *Keyspace) ZRank(key, member string, reverse bool) (int64, bool, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return 0, false, nil
	}
	if current.kind != KindSortedSet {
		return 0, false, ErrWrongType
	}
	score, exists := current.sortedSet.scores[member]
	if !exists {
		return 0, false, nil
	}
	rank := current.sortedSet.index.rank(score, member)
	if reverse {
		rank = current.sortedSet.index.length - 1 - rank
	}
	return int64(rank), true, nil
}

func (k *Keyspace) ZRangeByRank(key string, start, stop int64, reverse bool) ([]ZItem, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return []ZItem{}, nil
	}
	if current.kind != KindSortedSet {
		return nil, ErrWrongType
	}
	start, stop, ok := normalizeRankRange(int64(current.sortedSet.index.length), start, stop)
	if !ok {
		return []ZItem{}, nil
	}

	items := make([]ZItem, 0, stop-start+1)
	absoluteRank := uint64(start)
	if reverse {
		absoluteRank = current.sortedSet.index.length - 1 - uint64(start)
	}
	node := current.sortedSet.index.nodeByRank(absoluteRank)
	for rank := start; rank <= stop && node != nil; rank++ {
		items = append(items, ZItem{Member: node.member, Score: node.score})
		if reverse {
			node = node.backward
		} else {
			node = node.levels[0].forward
		}
	}
	return items, nil
}

func (k *Keyspace) ZRangeByScore(
	key string,
	minimum, maximum ScoreBoundary,
	reverse bool,
	offset, count int64,
) ([]ZItem, error) {
	k.mutex.Lock()
	defer k.mutex.Unlock()

	current, exists := k.liveEntryLocked(key, k.clock.Now())
	if !exists {
		return []ZItem{}, nil
	}
	if current.kind != KindSortedSet {
		return nil, ErrWrongType
	}
	if minimum.Value > maximum.Value ||
		(minimum.Value == maximum.Value && (minimum.Exclusive || maximum.Exclusive)) ||
		count == 0 {
		return []ZItem{}, nil
	}

	var node *skipListNode
	if reverse {
		node = current.sortedSet.index.lastInScoreRange(maximum)
	} else {
		node = current.sortedSet.index.firstInScoreRange(minimum)
	}
	for node != nil && offset > 0 && scoreWithinRange(node.score, minimum, maximum) {
		offset--
		if reverse {
			node = node.backward
		} else {
			node = node.levels[0].forward
		}
	}

	items := make([]ZItem, 0)
	for node != nil && scoreWithinRange(node.score, minimum, maximum) && (count < 0 || int64(len(items)) < count) {
		items = append(items, ZItem{Member: node.member, Score: node.score})
		if reverse {
			node = node.backward
		} else {
			node = node.levels[0].forward
		}
	}
	return items, nil
}

func normalizeRankRange(length, start, stop int64) (int64, int64, bool) {
	if start < 0 {
		start = length + start
	}
	if stop < 0 {
		stop = length + stop
	}
	if start < 0 {
		start = 0
	}
	if stop < 0 || start >= length || start > stop {
		return 0, 0, false
	}
	if stop >= length {
		stop = length - 1
	}
	return start, stop, true
}

func scoreWithinRange(score float64, minimum, maximum ScoreBoundary) bool {
	return minimum.includesLower(score) && maximum.includesUpper(score)
}
