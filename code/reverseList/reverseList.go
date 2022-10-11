package main

type ListNode struct {
	Val  int
	Next *ListNode
}

func reverseList(head *ListNode) *ListNode {
	var pre *ListNode
	cur := head
	for cur != nil {
		tmp := cur.Next
		cur.Next = pre
		pre = cur
		cur = tmp
	}
	return pre
}

func reverseList1(head *ListNode) *ListNode {
	if head == nil || head.Next == nil {
		return head
	}
	tmp := reverseList(head.Next)
	head.Next.Next = head
	head.Next = nil
	return tmp
}

func reverseKGroup(head *ListNode, k int) *ListNode {
	hair := &ListNode{Next: head}
	pre := hair

	for head != nil {
		tail := pre
		for i := 0; i < k; i++ {
			tail = tail.Next
			if tail == nil {
				return hair.Next
			}
		}
		nex := tail.Next
		head, tail = myReverse(head, tail)
		pre.Next = head
		tail.Next = nex
		pre = tail
		head = tail.Next
	}
	return hair.Next
}

func myReverse(head, tail *ListNode) (*ListNode, *ListNode) {
	prev := tail.Next
	p := head
	for prev != tail {
		nex := p.Next
		p.Next = prev
		prev = p
		p = nex
	}
	return tail, head
}

func rev(head *ListNode, k int) *ListNode {
	hair := &ListNode{Next: head}
	pre := hair
	for head != nil {
		tail := pre
		for i := 0; i < k; i++ {
			tail = tail.Next
			if tail == nil {
				return hair.Next
			}
		}
		next := tail.Next
		head, tail := merv(head, tail)
		pre.Next = head
		tail.Next = next
		pre = tail
		head = tail.Next
	}
	return hair.Next
}

func merv(head, tail *ListNode) (*ListNode, *ListNode) {
	pre := tail.Next
	cur := head
	for pre != tail {
		tmp := cur.Next
		cur.Next = pre
		pre = cur
		cur = tmp
	}
	return tail, head
}
