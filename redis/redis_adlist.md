# redis双端链表
相关文件
- adlist.h
- adlist.c

## 数据结构
节点结构中包含两个指针分别指向前后节点，value字段为void指针，可以保存任何类型的数据。

list结构体中包含头尾指针以及链表长度，其中包含三个函数的指针，因此对于每一个list可以自定义dup，free，match函数，实现复制，销毁以及匹配操作。

迭代器包含一个指向下一个元素的指针，以及迭代的方向。
```c
typedef struct listNode {
    struct listNode *prev;
    struct listNode *next;
    void *value;
} listNode;

typedef struct listIter {
    listNode *next;
    int direction;
} listIter;

typedef struct list {
    listNode *head;
    listNode *tail;
    void *(*dup)(void *ptr);
    void (*free)(void *ptr);
    int (*match)(void *ptr, void *key);
    unsigned long len;
} list;
```

## 基本原理

提供的功能比较简单，只是一个基本的双向链表
```c
// 创建链表申请空间，所有成员置零值
list *listCreate(void);

// 释放整个链表(包括节点与list结构的内存)，不会失败
// 先调用empty，在释放list结构
void listRelease(list *list);

// 清空list，顺序遍历所有节点，先调用自定义的free，再销毁内存O(N)
void listEmpty(list *list);

// 修改指针，调用free，释放内存
void listDelNode(list *list, listNode *node);

// 申请内存，放到头部
list *listAddNodeHead(list *list, void *value);

// 申请内存，放到尾部，因为保存了尾指针O(1)
list *listAddNodeTail(list *list, void *value);

// 插入到指定节点位置，after表示前后，0前
list *listInsertNode(list *list, listNode *old_node, void *value, int after);

// 创建一个iterator，指定从头还是尾开始，并保存方向
listIter *listGetIterator(list *list, int direction);

// 从头开始返回next；从尾开始返回prev
listNode *listNext(listIter *iter);

// 释放iterator内存
void listReleaseIterator(listIter *iter);

// 重置迭代器到链表头
void listRewind(list *list, listIter *li);

// 重置迭代器到链表尾
void listRewindTail(list *list, listIter *li);

// 申请新空间，复制所有属性，有dup，执行dup函数
list *listDup(list *orig);

// 顺序遍历，比较key，使用macth函数，没有的话直接key==node->value
listNode *listSearchKey(list *list, void *key);

// 正数正向，负数反向，超范围null
listNode *listIndex(list *list, long index);

// 尾移到头
void listRotateTailToHead(list *list);

// 头移到尾
void listRotateHeadToTail(list *list);

// 将o连接到l后面，o会被置空，但不会释放内存
void listJoin(list *l, list *o);
```