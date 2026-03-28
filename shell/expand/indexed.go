package expand

import "slices"

func denseIndices(n int) []int {
	if n <= 0 {
		return nil
	}
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices
}

func isDenseIndices(indices []int) bool {
	for i, index := range indices {
		if index != i {
			return false
		}
	}
	return true
}

func normalizeIndexed(list []string, indices []int) ([]string, []int) {
	if len(list) == 0 {
		return nil, nil
	}
	if len(indices) == 0 || isDenseIndices(indices) {
		return list, nil
	}
	return list, indices
}

func (v Variable) IndexedCount() int {
	return len(v.List)
}

func (v Variable) IndexedIndices() []int {
	if v.Kind != Indexed || len(v.List) == 0 {
		return nil
	}
	if v.Indices == nil {
		return denseIndices(len(v.List))
	}
	return slices.Clone(v.Indices)
}

func (v Variable) IndexedValues() []string {
	if v.Kind != Indexed || len(v.List) == 0 {
		return nil
	}
	return slices.Clone(v.List)
}

func (v Variable) IndexedMaxIndex() (int, bool) {
	if v.Kind != Indexed || len(v.List) == 0 {
		return 0, false
	}
	if v.Indices == nil {
		return len(v.List) - 1, true
	}
	return v.Indices[len(v.Indices)-1], true
}

func (v Variable) IndexedAppendIndex() int {
	maxIndex, ok := v.IndexedMaxIndex()
	if !ok {
		return 0
	}
	return maxIndex + 1
}

func (v Variable) IndexedResolve(index int) (int, bool) {
	if index >= 0 {
		return index, true
	}
	maxIndex, ok := v.IndexedMaxIndex()
	if !ok {
		return 0, false
	}
	index = maxIndex + 1 + index
	if index < 0 {
		return 0, false
	}
	return index, true
}

func (v Variable) indexedSearch(index int) (int, bool) {
	if v.Indices == nil {
		if index >= 0 && index < len(v.List) {
			return index, true
		}
		if index < 0 {
			return 0, false
		}
		if index > len(v.List) {
			return len(v.List), false
		}
		return index, false
	}
	return slices.BinarySearch(v.Indices, index)
}

func (v Variable) IndexedGet(index int) (string, bool) {
	if v.Kind != Indexed || index < 0 {
		return "", false
	}
	pos, ok := v.indexedSearch(index)
	if !ok {
		return "", false
	}
	return v.List[pos], true
}

func (v Variable) IndexedSet(index int, value string, appendValue bool) Variable {
	v.Kind = Indexed
	list := slices.Clone(v.List)
	indices := slices.Clone(v.Indices)
	pos, found := v.indexedSearch(index)
	if found {
		if appendValue {
			list[pos] += value
		} else {
			list[pos] = value
		}
		v.List, v.Indices = normalizeIndexed(list, indices)
		v.Set = len(v.List) > 0
		return v
	}
	if indices == nil && index > len(list) {
		indices = denseIndices(len(list))
		if indices == nil {
			indices = []int{}
		}
	}
	if indices == nil {
		list = slices.Insert(list, pos, value)
	} else {
		list = slices.Insert(list, pos, value)
		indices = slices.Insert(indices, pos, index)
	}
	v.List, v.Indices = normalizeIndexed(list, indices)
	v.Set = len(v.List) > 0
	return v
}

func (v Variable) IndexedUnset(index int) Variable {
	if v.Kind != Indexed || index < 0 {
		return v
	}
	pos, ok := v.indexedSearch(index)
	if !ok {
		return v
	}
	list := slices.Delete(slices.Clone(v.List), pos, pos+1)
	var indices []int
	if v.Indices != nil {
		indices = slices.Delete(slices.Clone(v.Indices), pos, pos+1)
	} else {
		indices = denseIndices(len(v.List))
		indices = slices.Delete(indices, pos, pos+1)
	}
	v.List, v.Indices = normalizeIndexed(list, indices)
	v.Set = len(v.List) > 0
	return v
}

func AssociativeKeys(m map[string]string) []string {
	return sortedMapKeys(m)
}

func AssociativeValues(m map[string]string) []string {
	return sortedMapValues(m)
}
