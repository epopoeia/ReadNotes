package nsum

import "sort"

func twoSum(nums []int, target int) []int {
	tmp := make(map[int]int)
	for i := range nums {
		y := target - nums[i]
		if ans, ok := tmp[y]; ok {
			if ans != i {
				return []int{i, ans}
			}
		}
		tmp[nums[i]] = i
	}
	return nil
}

func threeSum(nums []int, target int) [][]int {
	n := len(nums)
	sort.Ints(nums)
	ans := make([][]int, 0)
	for first := 0; first < n; first++ {
		if first > 0 && nums[first] == nums[first-1] {
			continue
		}
		third := n - 1
		target1 := target - nums[first]
		for second := first + 1; second < n; second++ {
			if second > first+1 && nums[second] == nums[second-1] {
				continue
			}
			for second < third && nums[second]+nums[third] > target1 {
				third--
			}
			if second == third {
				break
			}
			if nums[second]+nums[third] == target1 {
				ans = append(ans, []int{nums[first], nums[second], nums[third]})
			}
		}
	}
	return ans
}
