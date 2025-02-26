package hashring

import (
	"crypto/md5"
	"fmt"
	"hash"
	"math"
	"sort"
	"strconv"
	"sync"
)

type HashKey uint32
type HashKeyOrder []HashKey

func (h HashKeyOrder) Len() int           { return len(h) }
func (h HashKeyOrder) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h HashKeyOrder) Less(i, j int) bool { return h[i] < h[j] }

type HashRing struct {
	ring       map[HashKey]string
	sortedKeys []HashKey
	nodes      []string
	weights    map[string]int
	hasher     hash.Hash
	mtx        sync.RWMutex
}

func New(nodes []string) *HashRing {
	rh, _ := NewWithHash(nodes, md5.New())
	return rh
}
func NewWithHash(nodes []string, hasher hash.Hash) (*HashRing, error) {
	return new(nodes, make(map[string]int), hasher)
}

func NewWithHashAndWeights(weights map[string]int, hasher hash.Hash) (*HashRing, error) {
	nodes := make([]string, 0, len(weights))
	for node, _ := range weights {
		nodes = append(nodes, node)
	}
	return new(nodes, weights, hasher)
}

func NewWithWeights(weights map[string]int) *HashRing {
	rh, _ := NewWithHashAndWeights(weights, md5.New())
	return rh
}

func new(nodes []string, weights map[string]int, hasher hash.Hash) (*HashRing, error) {
	if hasher == nil {
		return nil, fmt.Errorf("hasher is nil")
	}
	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hasher:     hasher,
	}
	hashRing.generateCircle()
	return hashRing, nil
}

func (h *HashRing) Size() int {
	h.mtx.RLock()
	defer h.mtx.RUnlock()
	return len(h.nodes)
}

func (h *HashRing) UpdateWithWeights(weights map[string]int) {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	nodesChgFlg := false
	if len(weights) != len(h.weights) {
		nodesChgFlg = true
	} else {
		for node, newWeight := range weights {
			oldWeight, ok := h.weights[node]
			if !ok || oldWeight != newWeight {
				nodesChgFlg = true
				break
			}
		}
	}

	if nodesChgFlg {
		newhring, _ := NewWithHashAndWeights(weights, h.hasher)
		h.weights = newhring.weights
		h.nodes = newhring.nodes
		h.ring = newhring.ring
		h.sortedKeys = newhring.sortedKeys
	}
}

func (h *HashRing) generateCircle() {
	totalWeight := 0
	for _, node := range h.nodes {
		if weight, ok := h.weights[node]; ok {
			totalWeight += weight
		} else {
			totalWeight += 1
			h.weights[node] = 1
		}
	}

	for _, node := range h.nodes {
		weight := h.weights[node]

		factor := math.Floor(float64(40*len(h.nodes)*weight) / float64(totalWeight))

		for j := 0; j < int(factor); j++ {
			nodeKey := node + "-" + strconv.FormatInt(int64(j), 10)
			bKey := h.hashDigest(nodeKey)

			for i := 0; i < 3; i++ {
				key := hashVal(bKey[i*4 : i*4+4])
				h.ring[key] = node
				h.sortedKeys = append(h.sortedKeys, key)
			}
		}
	}

	sort.Sort(HashKeyOrder(h.sortedKeys))
}

func (h *HashRing) GetNode(stringKey string) (node string, ok bool) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()
	pos, ok := h.getNodePos(stringKey)
	if !ok {
		return "", false
	}
	return h.ring[h.sortedKeys[pos]], true
}

func (h *HashRing) GetNodePos(stringKey string) (pos int, ok bool) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()
	return h.getNodePos(stringKey)
}

func (h *HashRing) getNodePos(stringKey string) (pos int, ok bool) {
	if len(h.ring) == 0 {
		return 0, false
	}

	key := h.genKey(stringKey)

	nodes := h.sortedKeys
	pos = sort.Search(len(nodes), func(i int) bool { return nodes[i] > key })

	if pos == len(nodes) {
		// Wrap the search, should return first node
		return 0, true
	} else {
		return pos, true
	}
}

func (h *HashRing) GenKey(key string) HashKey {
	h.mtx.RLock()
	defer h.mtx.RUnlock()
	return h.genKey(key)
}

func (h *HashRing) genKey(key string) HashKey {
	bKey := h.hashDigest(key)
	return hashVal(bKey[0:4])
}

func (h *HashRing) GetNodes(stringKey string, size int) (nodes []string, ok bool) {
	h.mtx.RLock()
	defer h.mtx.RUnlock()
	pos, ok := h.getNodePos(stringKey)
	if !ok {
		return nil, false
	}

	if size > len(h.nodes) {
		return nil, false
	}

	returnedValues := make(map[string]bool, size)
	//mergedSortedKeys := append(h.sortedKeys[pos:], h.sortedKeys[:pos]...)
	resultSlice := make([]string, 0, size)

	for i := pos; i < pos+len(h.sortedKeys); i++ {
		key := h.sortedKeys[i%len(h.sortedKeys)]
		val := h.ring[key]
		if !returnedValues[val] {
			returnedValues[val] = true
			resultSlice = append(resultSlice, val)
		}
		if len(returnedValues) == size {
			break
		}
	}

	return resultSlice, len(resultSlice) == size
}

func (h *HashRing) AddNode(node string) *HashRing {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	return h.addWeightedNode(node, 1)
}

func (h *HashRing) AddWeightedNode(node string, weight int) *HashRing {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	return h.addWeightedNode(node, weight)
}

func (h *HashRing) addWeightedNode(node string, weight int) *HashRing {
	if weight <= 0 {
		return h
	}

	if _, ok := h.weights[node]; ok {
		return h
	}

	nodes := make([]string, len(h.nodes), len(h.nodes)+1)
	copy(nodes, h.nodes)
	nodes = append(nodes, node)

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		weights[eNode] = eWeight
	}
	weights[node] = weight

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hasher:     h.hasher,
	}
	hashRing.generateCircle()
	return hashRing
}

func (h *HashRing) UpdateWeightedNode(node string, weight int) *HashRing {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	if weight <= 0 {
		return h
	}

	/* node is not need to update for node is not existed or weight is not changed */
	if oldWeight, ok := h.weights[node]; (!ok) || (ok && oldWeight == weight) {
		return h
	}

	nodes := make([]string, len(h.nodes), len(h.nodes))
	copy(nodes, h.nodes)

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		weights[eNode] = eWeight
	}
	weights[node] = weight

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hasher:     h.hasher,
	}
	hashRing.generateCircle()
	return hashRing
}
func (h *HashRing) RemoveNode(node string) *HashRing {
	h.mtx.Lock()
	defer h.mtx.Unlock()
	/* if node isn't exist in hashring, don't refresh hashring */
	if _, ok := h.weights[node]; !ok {
		return h
	}

	nodes := make([]string, 0)
	for _, eNode := range h.nodes {
		if eNode != node {
			nodes = append(nodes, eNode)
		}
	}

	weights := make(map[string]int)
	for eNode, eWeight := range h.weights {
		if eNode != node {
			weights[eNode] = eWeight
		}
	}

	hashRing := &HashRing{
		ring:       make(map[HashKey]string),
		sortedKeys: make([]HashKey, 0),
		nodes:      nodes,
		weights:    weights,
		hasher:     h.hasher,
	}
	hashRing.generateCircle()
	return hashRing
}

func hashVal(bKey []byte) HashKey {
	return ((HashKey(bKey[3]) << 24) |
		(HashKey(bKey[2]) << 16) |
		(HashKey(bKey[1]) << 8) |
		(HashKey(bKey[0])))
}

func (h *HashRing) hashDigest(key string) []byte {
	return h.hasher.Sum([]byte(key))
}
