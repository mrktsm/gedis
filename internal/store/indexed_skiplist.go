package store

import (
	"math"
	"math/rand/v2"
)

const (
	skipListMaxLevel           = 32
	skipListPromotionThreshold = uint32(1 << 30) // 0.25 of the uint32 range.
	invalidSkipListRank        = ^uint64(0)
)

type skipListLevel struct {
	forward *skipListNode
	span    uint64
}

type skipListNode struct {
	member   string
	score    float64
	backward *skipListNode
	levels   []skipListLevel
}

type indexedSkipList struct {
	header *skipListNode
	tail   *skipListNode
	level  int
	length uint64
}

func newIndexedSkipList() *indexedSkipList {
	return &indexedSkipList{
		header: &skipListNode{levels: make([]skipListLevel, skipListMaxLevel)},
		level:  1,
	}
}

func (s *indexedSkipList) insert(score float64, member string) bool {
	if math.IsNaN(score) {
		return false
	}

	update := make([]*skipListNode, skipListMaxLevel)
	ranks := make([]uint64, skipListMaxLevel)
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		if level == s.level-1 {
			ranks[level] = 0
		} else {
			ranks[level] = ranks[level+1]
		}
		for current.levels[level].forward != nil && lessSkipListNode(
			current.levels[level].forward,
			score,
			member,
		) {
			ranks[level] += current.levels[level].span
			current = current.levels[level].forward
		}
		update[level] = current
	}

	candidate := update[0].levels[0].forward
	if candidate != nil && candidate.score == score && candidate.member == member {
		return false
	}

	nodeLevel := randomSkipListLevel()
	if nodeLevel > s.level {
		for level := s.level; level < nodeLevel; level++ {
			ranks[level] = 0
			update[level] = s.header
			update[level].levels[level].span = s.length
		}
		s.level = nodeLevel
	}

	node := &skipListNode{
		member: member,
		score:  score,
		levels: make([]skipListLevel, nodeLevel),
	}
	for level := 0; level < nodeLevel; level++ {
		node.levels[level].forward = update[level].levels[level].forward
		node.levels[level].span = update[level].levels[level].span - (ranks[0] - ranks[level])
		update[level].levels[level].forward = node
		update[level].levels[level].span = (ranks[0] - ranks[level]) + 1
	}
	for level := nodeLevel; level < s.level; level++ {
		update[level].levels[level].span++
	}

	if update[0] == s.header {
		node.backward = nil
	} else {
		node.backward = update[0]
	}
	if node.levels[0].forward != nil {
		node.levels[0].forward.backward = node
	} else {
		s.tail = node
	}
	s.length++
	return true
}

func (s *indexedSkipList) delete(score float64, member string) bool {
	update := make([]*skipListNode, skipListMaxLevel)
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		for current.levels[level].forward != nil && lessSkipListNode(
			current.levels[level].forward,
			score,
			member,
		) {
			current = current.levels[level].forward
		}
		update[level] = current
	}

	target := current.levels[0].forward
	if target == nil || target.score != score || target.member != member {
		return false
	}

	for level := 0; level < s.level; level++ {
		if update[level].levels[level].forward == target {
			update[level].levels[level].span += target.levels[level].span - 1
			update[level].levels[level].forward = target.levels[level].forward
		} else {
			update[level].levels[level].span--
		}
	}
	if target.levels[0].forward != nil {
		target.levels[0].forward.backward = target.backward
	} else {
		s.tail = target.backward
	}
	for s.level > 1 && s.header.levels[s.level-1].forward == nil {
		s.header.levels[s.level-1].span = 0
		s.level--
	}
	s.length--
	return true
}

// nodeByRank returns a node by zero-based ascending rank.
func (s *indexedSkipList) nodeByRank(rank uint64) *skipListNode {
	if rank >= s.length {
		return nil
	}

	target := rank + 1
	traversed := uint64(0)
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		for current.levels[level].forward != nil &&
			traversed+current.levels[level].span <= target {
			traversed += current.levels[level].span
			current = current.levels[level].forward
		}
		if traversed == target {
			return current
		}
	}
	return nil
}

// rank returns a member's zero-based ascending rank.
func (s *indexedSkipList) rank(score float64, member string) uint64 {
	traversed := uint64(0)
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		for current.levels[level].forward != nil && !lessScoreMember(
			score,
			member,
			current.levels[level].forward.score,
			current.levels[level].forward.member,
		) {
			traversed += current.levels[level].span
			current = current.levels[level].forward
		}
		if current != s.header && current.score == score && current.member == member {
			return traversed - 1
		}
	}
	return invalidSkipListRank
}

func (s *indexedSkipList) firstInScoreRange(minimum ScoreBoundary) *skipListNode {
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		for current.levels[level].forward != nil && !minimum.includesLower(
			current.levels[level].forward.score,
		) {
			current = current.levels[level].forward
		}
	}
	return current.levels[0].forward
}

func (s *indexedSkipList) lastInScoreRange(maximum ScoreBoundary) *skipListNode {
	current := s.header
	for level := s.level - 1; level >= 0; level-- {
		for current.levels[level].forward != nil && maximum.includesUpper(
			current.levels[level].forward.score,
		) {
			current = current.levels[level].forward
		}
	}
	if current == s.header {
		return nil
	}
	return current
}

func lessSkipListNode(node *skipListNode, score float64, member string) bool {
	return lessScoreMember(node.score, node.member, score, member)
}

func lessScoreMember(firstScore float64, firstMember string, secondScore float64, secondMember string) bool {
	if firstScore != secondScore {
		return firstScore < secondScore
	}
	return firstMember < secondMember
}

func randomSkipListLevel() int {
	level := 1
	for level < skipListMaxLevel && rand.Uint32() < skipListPromotionThreshold {
		level++
	}
	return level
}

type ScoreBoundary struct {
	Value     float64
	Exclusive bool
}

func (b ScoreBoundary) includesLower(score float64) bool {
	if b.Exclusive {
		return score > b.Value
	}
	return score >= b.Value
}

func (b ScoreBoundary) includesUpper(score float64) bool {
	if b.Exclusive {
		return score < b.Value
	}
	return score <= b.Value
}
