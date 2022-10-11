package main

import "fmt"

type stack struct {
	d []int
}

func (s *stack) pop() int {
	a := s.d[len(s.d)-1]
	s.d = s.d[:len(s.d)-1]
	return a
}

func (s *stack) push(a int) {
	s.d = append(s.d, a)
}

func (s *stack) peak() int {
	return s.d[len(s.d)-1]
}
func (s *stack) isEmpty() bool {
	return len(s.d) == 0
}

func main() {
	a := []int{8, 2, 5, 4, 3, 9, 7, 2, 5}
	result := make([]int, len(a))
	s := &stack{d: make([]int, 0)}
	s.push(0)
	i := 1
	for i < len(a) {
		if !s.isEmpty() && a[i] > a[s.peak()] {
			result[s.pop()] = a[i]
		} else {
			s.push(i)
			i++
		}
	}
	for !s.isEmpty() {
		result[s.pop()] = -1
	}
	fmt.Println(result)

}
