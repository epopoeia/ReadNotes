package kthlargest

import "fmt"

func findKthLargest(nums []int, k int) int {
	left := 0
	right := len(nums) - 1

	for {
		if left >= right { // 重要
			return nums[right]
		}
		p := partition(nums, left, right)
		if p+1 == k {
			return nums[p]
		} else if p+1 < k {
			left = p + 1
		} else {
			right = p - 1
		}
	}

}

func partition(nums []int, left int, right int) int {
	pivot := nums[right]
	for i := left; i < right; i++ {
		if nums[i] > pivot {
			nums[left], nums[i] = nums[i], nums[left]
			left++
		}
	}
	nums[left], nums[right] = nums[right], nums[left]
	return left
}

func findKthLargest1(nums []int, k int) int {
	heapSize := len(nums)
	buildMaxHeap(nums, heapSize)
	for i := len(nums) - 1; i >= len(nums)-k+1; i-- {
		nums[0], nums[i] = nums[i], nums[0]
		heapSize--
		maxHeapify(nums, 0, heapSize)
	}
	return nums[0]
}

func buildMaxHeap(a []int, heapSize int) {
	for i := heapSize / 2; i >= 0; i-- {
		maxHeapify(a, i, heapSize)
	}
}

func maxHeapify(a []int, i, heapSize int) {
	l, r, largest := i*2+1, i*2+2, i
	if l < heapSize && a[l] > a[largest] {
		largest = l
	}
	if r < heapSize && a[r] > a[largest] {
		largest = r
	}
	if largest != i {
		a[i], a[largest] = a[largest], a[i]
		maxHeapify(a, largest, heapSize)
	}
}

func findKthLargest2(nums []int, k int) int {
	a := make([]int, k)
	l := 0
	for i := range nums {
		e := nums[i]
		if l < k {
			a[l] = e
			i := l
			for i > 0 {
				parent := (i - 1) / 2
				if a[parent] <= e {
					break
				}
				a[i] = a[parent]
				i = parent
			}
			a[i] = e
			l++
		} else {
			if nums[i] < a[0] {
				continue
			}
			a[0] = nums[i]
			minHeapfy(a, 0, k)
		}
	}
	fmt.Println(a)
	return a[0]
}

func minHeapfy(a []int, i, heapSize int) {
	l, r, min := 2*i+1, i*2+2, i
	if l < heapSize && a[l] < a[min] {
		min = l
	}
	if r < heapSize && a[r] < a[min] {
		min = r
	}
	if min != i {
		a[i], a[min] = a[min], a[i]
		minHeapfy(a, min, heapSize)
	}
}
