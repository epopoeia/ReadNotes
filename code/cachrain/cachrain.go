package cacherain

func trap(height []int) int {
	if len(height) == 0 {
		return 0
	}
	ans := 0
	leftMax := make([]int, len(height))
	rightMax := make([]int, len(height))
	for i := range height {
		if i == 0 {
			leftMax[i] = height[i]
		} else {
			leftMax[i] = max(height[i], leftMax[i-1])
		}
	}
	for i := len(height) - 1; i >= 0; i-- {
		if i == len(height)-1 {
			rightMax[i] = height[i]
		} else {
			rightMax[i] = max(height[i], rightMax[i+1])
		}
	}

	for r := range height {
		ans += min(leftMax[r], rightMax[r]) - height[r]
	}
	return ans
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func trap1(height []int) int {
	left, right := 0, len(height)-1
	leftMax, rightMax := 0, 0
	ans := 0
	for left < right {
		if height[left] < height[right] {
			if height[left] > leftMax {
				leftMax = height[left]
			} else {
				ans += leftMax - height[left]
			}
			left++
		} else {
			if height[right] > rightMax {
				rightMax = height[right]
			} else {
				ans += rightMax - height[right]
			}
			right--
		}
	}
	return ans
}
