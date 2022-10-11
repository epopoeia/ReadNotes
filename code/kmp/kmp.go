package kmp

func genNext1(s string) []int {
	next := make([]int, len(s))
	for i := range s {
		if i == 0 {
			next[i] = -1
		} else if i == 1 {
			next[i] = 0
		} else {
			n := 0
			for j := i / 2; j > 0; j-- {
				if s[0:j] == s[i-j:i] {
					n = j
					break
				}
			}
			next[i] = n
		}
	}
	return next
}

func genNext(s string) []int {
	next := make([]int, len(s))
	next[0] = -1
	j := -1
	for i := 0; i < len(s)-1; {
		if j == -1 || s[i] == s[j] {
			i++
			j++
			next[i] = j
		} else {
			j = next[j]
		}
	}
	return next
}

func KMSearch(target, text string) int {
	next := genNext(target)
	i, j := 0, 0
	for i < len(text) && j < len(target) {
		if i == len(target)-1 && target[i] == text[j] {
			return j - i
		}
		if i == -1 || target[i] == text[j] {
			j++
			i++
		} else {
			i = next[i]
		}
	}
	return -1
}
