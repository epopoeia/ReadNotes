// package main

// import (
// 	"fmt"
// 	"math"
// 	"unsafe"
// )

// func main() {
// 	times := [][]int{{1, 2, 1}, {1, 3, 2}, {2, 5, 8}, {2, 4, 5}, {3, 4, 3}, {3, 6, 10}, {4, 5, 1}, {4, 6, 1}}
// 	fmt.Println(networkDelayTime1(times, 6, 1))
// }

// func byte2string(b []byte) string {
// 	return *(*string)(unsafe.Pointer(&b))
// }

// func string2byte(s string) []byte {
// 	x := (*[2]uintptr)(unsafe.Pointer(&s))
// 	h := [3]uintptr{x[0], x[1], x[1]}
// 	return *(*[]byte)(unsafe.Pointer(&h))
// }

// func networkDelayTime(times [][]int, N int, K int) int {
// 	max := 0
// 	mGraph := make([][]int, N)
// 	// K到各个节点的最短时间，dist使用优先队列存储，存在最小堆中
// 	dist := make([]int, N)
// 	// 初始化优先队列，初始化位置dist=0，其他位置正无穷
// 	for i := 0; i < N; i++ {
// 		mGraph[i] = make([]int, N)
// 		for j := 0; j < N; j++ {
// 			mGraph[i][j] = -1
// 		}
// 		dist[i] = math.MaxInt64
// 	}

// 	// 初始化第一个元素
// 	dist[K-1] = 0
// 	// 构建图
// 	for i := range times {
// 		mGraph[times[i][0]-1][times[i][1]-1] = times[i][2]
// 	}
// 	// 已经访问的最短路径的顶点
// 	visted := make(map[int]bool)
// 	// 初始化堆
// 	heap := &NodeDistMinHeap{}
// 	heap.Push(NodeDist{node: K - 1, dist: 0})
// 	for heap.Len() != 0 {
// 		x := heap.Pop()
// 		v := x.(NodeDist).node
// 		if visted[v] {
// 			continue
// 		}
// 		visted[v] = true
// 		for w := range mGraph[v] {
// 			if !visted[w] && mGraph[v][w] >= 0 {
// 				if dist[v]+mGraph[v][w] < dist[w] {
// 					dist[w] = dist[v] + mGraph[v][w]
// 					heap.Push(NodeDist{node: w, dist: dist[w]})
// 				}
// 			}
// 		}
// 		fmt.Println(dist)
// 	}
// 	for i := range dist {
// 		if dist[i] == math.MaxInt64 {
// 			return -1
// 		}
// 		if dist[i] > max {
// 			max = dist[i]
// 		}
// 	}
// 	fmt.Println(dist)

// 	return max
// }

// type NodeDist struct {
// 	node int
// 	dist int
// }

// type NodeDistMinHeap []NodeDist

// func (h *NodeDistMinHeap) Len() int {
// 	return len(*h)
// }

// func (h *NodeDistMinHeap) Less(i, j int) bool {
// 	return (*h)[i].dist < (*h)[j].dist
// }

// func (h *NodeDistMinHeap) Swap(i, j int) {
// 	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
// }

// func (h *NodeDistMinHeap) Push(x interface{}) {
// 	*h = append(*h, x.(NodeDist))
// 	i := len(*h) - 1
// 	for i > 0 {
// 		parent := (i - 1) / 2
// 		if h.Less(parent, i) {
// 			break
// 		}
// 		h.Swap(parent, i)
// 		i = parent
// 	}
// }

// func (h *NodeDistMinHeap) Pop() interface{} {
// 	x := (*h)[0]
// 	h.Swap(0, len(*h)-1)
// 	(*h) = (*h)[:len(*h)-1]
// 	h.minHeapfy(0)
// 	return x
// }

// func (h *NodeDistMinHeap) Peek() NodeDist {
// 	return (*h)[0]
// }

// func (h *NodeDistMinHeap) minHeapfy(i int) {
// 	l, r, min := 2*i+1, i*2+2, i
// 	if l < len(*h) && h.Less(l, min) {
// 		min = l
// 	}
// 	if r < len(*h) && h.Less(r, min) {
// 		min = r
// 	}
// 	if min != i {
// 		h.Swap(i, min)
// 		h.minHeapfy(min)
// 	}
// }

// func networkDelayTime1(times [][]int, N int, K int) int {
// 	max := 0
// 	mGraph := make([][]int, N)
// 	// K到各个节点的最短时间，dist使用优先队列存储，存在最小堆中
// 	dist := make([]int, N)
// 	// 初始化优先队列，初始化位置dist=0，其他位置正无穷
// 	for i := 0; i < N; i++ {
// 		mGraph[i] = make([]int, N)
// 		for j := 0; j < N; j++ {
// 			mGraph[i][j] = -1
// 		}
// 		dist[i] = math.MaxInt64
// 	}

// 	// 初始化第一个元素
// 	dist[K-1] = 0
// 	// 构建图
// 	for i := range times {
// 		mGraph[times[i][0]-1][times[i][1]-1] = times[i][2]
// 	}
// 	// 已经访问的最短路径的顶点
// 	visted := make(map[int]bool)
// 	for v := 0; v < N; v++ {
// 		t := -1
// 		for j := 0; j < N; j++ {
// 			if !visted[j] && (t == -1 || dist[t] > dist[j]) {
// 				t = j
// 			}
// 		}
// 		visted[t] = true
// 		for w := range mGraph[t] {
// 			if mGraph[t][w] >= 0 {
// 				if dist[t]+mGraph[t][w] < dist[w] {
// 					dist[w] = dist[t] + mGraph[t][w]
// 				}
// 				fmt.Println(dist)
// 			}
// 		}
// 	}
// 	for i := range dist {
// 		if dist[i] == math.MaxInt64 {
// 			return -1
// 		}
// 		if dist[i] > max {
// 			max = dist[i]
// 		}
// 	}
// 	fmt.Println(dist)
// 	return max
// }

package main

import _ "embed"

//go:embed hello.txt
var s string

func main() {

	print(s)
}
