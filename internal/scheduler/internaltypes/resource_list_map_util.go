package internaltypes

import (
	"sort"
	"strings"

	"golang.org/x/exp/maps"
)

func RlMapToString(m map[string]ResourceList) string {
	keys := maps.Keys(m)
	sort.Strings(keys)
	results := []string{}
	for _, k := range keys {
		results = append(results, k+"="+m[k].String())
	}
	return strings.Join(results, " ")
}

func RlMapSumValues(m map[string]ResourceList) ResourceList {
	result := ResourceList{}
	for _, v := range m {
		result = result.Add(v)
	}
	return result
}

func RlMapAllZero(m map[string]ResourceList) bool {
	for _, v := range m {
		if !v.AllZero() {
			return false
		}
	}
	return true
}

func RlMapHasNegativeValues(m map[string]ResourceList) bool {
	for _, v := range m {
		if v.HasNegativeValues() {
			return true
		}
	}
	return false
}

func RlMapRemoveZeros(m map[string]ResourceList) map[string]ResourceList {
	result := map[string]ResourceList{}
	for k, v := range m {
		if !v.AllZero() {
			result[k] = v
		}
	}
	return result
}

func NewAllocatableByPriorityAndResourceType(priorities []int32, rl ResourceList) map[int32]ResourceList {
	result := map[int32]ResourceList{}
	for _, priority := range priorities {
		result[priority] = rl
	}
	result[EvictedPriority] = rl
	return result
}

// MarkAllocated indicates resources have been allocated to pods of priority p,
// hence reducing the resources allocatable to pods of priority p or lower.
func MarkAllocated(m map[int32]ResourceList, p int32, rs ResourceList) {
	MarkAllocatable(m, p, rs.Negate())
}

// MarkAllocatable indicates resources have been released by pods of priority p,
// thus increasing the resources allocatable to pods of priority p or lower.
func MarkAllocatable(m map[int32]ResourceList, p int32, rs ResourceList) {
	for priority, allocatableResourcesAtPriority := range m {
		if priority <= p {
			m[priority] = allocatableResourcesAtPriority.Add(rs)
		}
	}
}
