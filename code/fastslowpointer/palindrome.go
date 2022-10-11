package fastslowpointer

type ListNode struct {
	Val  int
	Next *ListNode
}

func isPalindrome(head *ListNode) bool {
	if head == nil || head.Next == nil {
		return true
	}
	fast, slow := head, head

	for fast != nil && fast.Next != nil {
		slow = slow.Next
		fast = fast.Next.Next
	}
	if fast != nil { // 奇数
		slow = slow.Next
	}
	var pre *ListNode
	for slow != nil {
		pre, slow, slow.Next = slow, slow.Next, pre
	}

	for pre != nil {
		if pre.Val != head.Val {
			return false
		}
		pre = pre.Next
		head = head.Next
	}
	return true
}
