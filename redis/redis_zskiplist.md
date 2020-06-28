# redis 跳跃表
相关文件
- server.h
- t_zset.c
## 基本特点
- 用来存放有序集合（zset），插入删除复杂度为O(log(N))
- 集合元素不能重复，但分值score可以重复
- 最高32（ZSKIPLIST_MAXLEVEL）

## 数据结构
```c
typedef struct zskiplistNode {
    sds ele;  // 与dict中数据指向同一地址，节省空间
    double score;
    struct zskiplistNode *backward;
    struct zskiplistLevel {
        struct zskiplistNode *forward;
        unsigned long span; // x->level[i].span表示x在第i层到下一个节点需要跳过的节点个数，相邻为1
    } level[];
} zskiplistNode;

typedef struct zskiplist {
    struct zskiplistNode *header, *tail;
    unsigned long length;
    int level;
} zskiplist;

typedef struct zset {
    dict *dict;
    zskiplist *zsl;
} zset;
```
由数据结构可以看出zset内部使用了dict以及zskiplist两个数据结构，插入zset中的元素会被同时插入到这两个结构中。
- dict：用来保存object->socre的映射
- zskiplist：用来保存score->object的映射，并且是按照sore排序的

zskiplist中的sds字段保存的值与dict中的值使用的是同一地址，可以节约内存，并且在释放内存时，直接使用zslFreeNode()释放当前节点即会释放sds的内存，但是在这之前必须先从dict中删除元素，从dict中删除元素并不会释放内存。

相对于原生的跳跃表，redis中的实现进行了三部分的修改
- 允许重复的score
- 比较时不比较key(score)，也会比较一些附加数据
- 在zskiplistNode中有一个backward指针，只有最底层的链表会设置这个指针，这个用来从尾部向前遍历方便实现zrevrange。

## skiplist原理

如果数据使用数组存储，则进行二分查找速度比较快，但是因为使用的时链表，并不能进行二分查找，因此继承二分查找的思想，将链表数据进行抽象，通过多层链表结构来加快查找速度。简单来说，最上层存储整个链表的中间节点以及头尾，相当于二等分，下一层四等分以此类推，最后一层存储完整的树数据。

在skiplist实现中引入了随机数，所以每层元素不会均匀分配。上面数据结构里的level数组表示了当前skiplist的层数，最大层数为32层。其中第0层为全量节点，越往上节点越少。并且一旦一个节点在某一层出现，其下面的每一层都会有。

根据分层的原理来看，层数越高包含的节点数就越少，每次插入会随机生成一个层数，这个层数以下的会被插入新元素，理想情况下分层结构应该像上面说的是2^n这种结构。因此高层的元素应该比较少，因此随机函数返回的level应该是小的数值频率会高一些。

```c
#define ZSKIPLIST_MAXLEVEL 32 /* Should be enough for 2^64 elements */
#define ZSKIPLIST_P 0.25      /* Skiplist P = 1/4 */

int zslRandomLevel(void) {
    int level = 1;
    while ((random()&0xFFFF) < (ZSKIPLIST_P * 0xFFFF))
        level += 1;
    return (level<ZSKIPLIST_MAXLEVEL) ? level : ZSKIPLIST_MAXLEVEL;
}
```
通过随机数计数器来返回层数，可以产生一个类似于指数的分布，并且大数不容易被返回，因为靠累加器。概率上可以保证上一层节点是下一层的1/4。返回1的概率0.75，返回2的概率0.25*0.75。因此32层最多能存储2^64个节点(4^32)。

### 创建过程

```c
zskiplist *zslCreate(void) {
    int j;
    zskiplist *zsl;

    zsl = zmalloc(sizeof(*zsl));
    zsl->level = 1;
    zsl->length = 0;
    zsl->header = zslCreateNode(ZSKIPLIST_MAXLEVEL,0,NULL);
    for (j = 0; j < ZSKIPLIST_MAXLEVEL; j++) {
        zsl->header->level[j].forward = NULL;
        zsl->header->level[j].span = 0;
    }
    zsl->header->backward = NULL;
    zsl->tail = NULL;
    return zsl;
}

zskiplistNode *zslCreateNode(int level, double score, sds ele) {
    zskiplistNode *zn =
        zmalloc(sizeof(*zn)+level*sizeof(struct zskiplistLevel));
    zn->score = score;
    zn->ele = ele;
    return zn;
}
```

创建过程比较简单，初始化整个链表的层数为1，并且会随着插入删除动态变化，创建一个头节点初始化level为最大值，并将每一层初始化为零值。

### 插入实现

```c
zskiplistNode *zslInsert(zskiplist *zsl, double score, sds ele) {
    // 记录查找过程中每一层找到的最右节点(找到插入位置的话即为第一个大于插入值的节点)
    zskiplistNode *update[ZSKIPLIST_MAXLEVEL], *x;
    // 记录每层寻找元素过程跳过过的节点数
    unsigned int rank[ZSKIPLIST_MAXLEVEL];
    int i, level;

    serverAssert(!isnan(score));
    x = zsl->header;
    // 记录沿途访问节点，累加span
    // 平均时间O(log N)，最坏情况下O(N)
    for (i = zsl->level-1; i >= 0; i--) {
        // 开始层为0，否则为高一层的值
        rank[i] = i == (zsl->level-1) ? 0 : rank[i+1];
        // 指向的下一个节点不为空
        while (x->level[i].forward &&
        // 下一个节点score小于待插入score
                (x->level[i].forward->score < score ||
                // score相同，但是sds比当前小
                    (x->level[i].forward->score == score &&
                    sdscmp(x->level[i].forward->ele,ele) < 0)))
        {
            // 记录经过的每一个节点跳过的元素数累加
            rank[i] += x->level[i].span;
            // 继续遍历
            x = x->level[i].forward;
        }
        // 理论上的插入节点位置，插入到x后面
        // 进入下一层时从x位置开始遍历
        update[i] = x;
    }
    // 代码里注释说这个函数不会被传入score以及value都相同的元素
    // 所以直接创建新节点

    // 计算随机层数
    level = zslRandomLevel();
    // 如果比当前层数大，则需要更新层数
    if (level > zsl->level) {
        // 新产生的高层只有头节点，相当于跳过了所有的节点，即span=len
        for (i = zsl->level; i < level; i++) {
            rank[i] = 0;
            update[i] = zsl->header;
            update[i]->level[i].span = zsl->length;
        }
        zsl->level = level;
    }
    x = zslCreateNode(level,score,ele);
    // 从最底层开始插入元素，O(N)
    for (i = 0; i < level; i++) {
        // 插入到当前层update后的位置
        x->level[i].forward = update[i]->level[i].forward;
        update[i]->level[i].forward = x;

        // 新节点到下一节点需要跳过的元素数
        x->level[i].span = update[i]->level[i].span - (rank[0] - rank[i]);
        // 更新节点到下一节点的元素数
        update[i]->level[i].span = (rank[0] - rank[i]) + 1;
    }

    // 上面几层没有插入元素，只需要增加以下跳过的元素数
    for (i = level; i < zsl->level; i++) {
        update[i]->level[i].span++;
    }

    // 只更新最下面一层的前指针
    x->backward = (update[0] == zsl->header) ? NULL : update[0];
    if (x->level[0].forward)
        x->level[0].forward->backward = x;
    else
        zsl->tail = x;
    zsl->length++;
    return x;
}
```

更新过程中呢，会从高层向底层遍历，每次进入下一层都从之前找到的插入点开始继续遍历，rank来记录每一层插入点跳过的元素树(插入点前所有节点跳过的元素数+插入点到下一节点跳过的元素数)，然后将元素插入到插入点之后。

这样记录就可以通过低层的的rank减去高层的rank就得到了高层插入点到底层插入点之间跳过的元素数设为a，当前层插入点到下一节点跳过的元素数设为b。b-a就得到了新节点在当前层到下一节点需要跳过的元素数；a+1就是插入点到新节点需要跳过的元素数。

### 删除节点的实现

```c

int zslDelete(zskiplist *zsl, double score, sds ele, zskiplistNode **node) {
    zskiplistNode *update[ZSKIPLIST_MAXLEVEL], *x;
    int i;
    // 查找节点过程与插入相同只是不需要记录跳过rank
    x = zsl->header;
    for (i = zsl->level-1; i >= 0; i--) {
        while (x->level[i].forward &&
                (x->level[i].forward->score < score ||
                    (x->level[i].forward->score == score &&
                     sdscmp(x->level[i].forward->ele,ele) < 0)))
        {
            x = x->level[i].forward;
        }
        update[i] = x;
    }

    // 由于存在score相同的节点，所以需要比较具体的value来确定节点(不存在score与value都相同的节点)
    x = x->level[0].forward;
    if (x && score == x->score && sdscmp(x->ele,ele) == 0) {
        zslDeleteNode(zsl, x, update);
        // 如果node指针为null则释放找到的节点，否则将x赋值给node指针
        if (!node)
            zslFreeNode(x);
        else
            *node = x;
        return 1;
    }
    return 0; /* not found */
}

// 从skpilist中删除节点
// update数组即为上面函数找到的每一次要修改的节点，即x的前一个节点
void zslDeleteNode(zskiplist *zsl, zskiplistNode *x, zskiplistNode **update) {
    int i;
    for (i = 0; i < zsl->level; i++) {
        // 如果在当前层的下一个节点即为x则修改当前层指针，否则只是跳过节点数-1
        if (update[i]->level[i].forward == x) {
            update[i]->level[i].span += x->level[i].span - 1;
            update[i]->level[i].forward = x->level[i].forward;
        } else {
            update[i]->level[i].span -= 1;
        }
    }
    // 修改原始数组中的back指针
    if (x->level[0].forward) {
        x->level[0].forward->backward = x->backward;
    } else {
        zsl->tail = x->backward;
    }
    // 如果最高层空了，就删掉这一层
    while(zsl->level > 1 && zsl->header->level[zsl->level-1].forward == NULL)
        zsl->level--;
    zsl->length--;
}
```

### 更新节点的score
更新跳跃表中某一个元素的score但是并不会更新字典中的score，这个需要调用者来更新。更新完score节点最好保持在原位置，不然会导致删除重新添加，这个操作成本比较高。最终返回更新节点的指针。
```c
zskiplistNode *zslUpdateScore(zskiplist *zsl, double curscore, sds ele, double newscore) {
    zskiplistNode *update[ZSKIPLIST_MAXLEVEL], *x;
    int i;

    // 定位要更新节点的前一个节点，为了重插入时修改指针使用
    x = zsl->header;
    for (i = zsl->level-1; i >= 0; i--) {
        while (x->level[i].forward &&
                (x->level[i].forward->score < curscore ||
                    (x->level[i].forward->score == curscore &&
                     sdscmp(x->level[i].forward->ele,ele) < 0)))
        {
            x = x->level[i].forward;
        }
        update[i] = x;
    }

    // 拿到我们要更新的节点
    x = x->level[0].forward;
    // 保证score相同，并且ele相同
    serverAssert(x && curscore == x->score && sdscmp(x->ele,ele) == 0);

   // 更新后的score不会影响插入位置，则直接修改
   // 新score比前面的大，比后面的小
    if ((x->backward == NULL || x->backward->score < newscore) &&
        (x->level[0].forward == NULL || x->level[0].forward->score > newscore))
    {
        x->score = newscore;
        return x;
    }

    // 到了这里，说明上面两条件有不满足的
    // 需要删除原节点重新插入，成本很高
    // 这里删除x节点，并没有释放x的空间
    zslDeleteNode(zsl, x, update);
    zskiplistNode *newnode = zslInsert(zsl,newscore,x->ele);
    // 插入的新节点用的时x->sds，要释放x节点，但是不能释放ele，所以置null
    x->ele = NULL;
    zslFreeNode(x);
    return newnode;
}
```

### 范围查找
zset主要作用之一就是按照范围进行查询，查询条件主要有两个结构。

```c
// 以score为条件的范围查询
typedef struct {
    double min, max;
    int minex, maxex; // 是否包含上下界
} zrangespec;

// 以字典序进行范围查询
typedef struct {
    sds min, max;     /* May be set to shared.(minstring|maxstring) */
    int minex, maxex; // 是否包含上下界
} zlexrangespec;
```

判断是否在范围内的函数很简单
```c
// 判断整个表是否有一部分在指定范围内
int zslIsInRange(zskiplist *zsl, zrangespec *range) {
    zskiplistNode *x;

    // 范围本身就不包含元素
    if (range->min > range->max ||
            (range->min == range->max && (range->minex || range->maxex)))
        return 0;
    x = zsl->tail;
    if (x == NULL || !zslValueGteMin(x->score,range))
        return 0;
    x = zsl->header->level[0].forward;
    if (x == NULL || !zslValueLteMax(x->score,range))
        return 0;
    return 1;
}
int zslValueGteMin(double value, zrangespec *spec) {
    return spec->minex ? (value > spec->min) : (value >= spec->min);
}

int zslValueLteMax(double value, zrangespec *spec) {
    return spec->maxex ? (value < spec->max) : (value <= spec->max);
}
```

当满足整个表有在范围内的节点时才会进行查找。对于字典序的范围判断同理，只是比较的不是score而是内部的sds。

```c
// 返回第一个在范围内的节点
zskiplistNode *zslFirstInRange(zskiplist *zsl, zrangespec *range)

// 返回最后一个在范围内的节点
// 同前面函数一样，从头开始遍历，找到x->forward大于score的节点之后，返回x
zskiplistNode *zslLastInRange(zskiplist *zsl, zrangespec *range)

// 字典序判断同理
zskiplistNode *zslFirstInLexRange(zskiplist *zsl, zlexrangespec *range)
zskiplistNode *zslLastInLexRange(zskiplist *zsl, zlexrangespec *range)

// 根据rank获取节点
// 从顶层开始遍历，累加span，直到遇到节点x，加上x.span后大于rank(此时并未改变累加值)
// 如果累加值刚好等于rank，返回x；否则以x为起点进入下一层继续遍历
// 如果累加值与rank最终也不相等返回null
zskiplistNode* zslGetElementByRank(zskiplist *zsl, unsigned long rank)

// 寻找score与ele都与参数相同的节点，返回rank值，否则返回0
// while中循环类似于插入时的查找，累加rank
unsigned long zslGetRank(zskiplist *zsl, double score, sds ele){
    zskiplistNode *x;
    unsigned long rank = 0;
    int i;

    x = zsl->header;
    for (i = zsl->level-1; i >= 0; i--) {
        // 与插入类似累加rank
        while (x->level[i].forward &&
            (x->level[i].forward->score < score ||
                (x->level[i].forward->score == score &&
                sdscmp(x->level[i].forward->ele,ele) <= 0))) {
            rank += x->level[i].span;
            x = x->level[i].forward;
        }
        // 在进入下一层前判断ele是否相等
        // 类似于上一个函数，进入下一层前判断累加值与rank是否相等
        if (x->ele && sdscmp(x->ele,ele) == 0) {
            return rank;
        }
    }
    return 0;
}
```

## zset的另一种实现

虽然跳跃表是用来实现zset的，但是zset还有一种实现，使用的是ziplist。之前ziplist中说了主要是用来存储小对象，性能比较高。因此zset利用了这一理念，在不同情况下会使用不同的实现。

满足下面条件时会使用ziplist实现。
- 元素数量小于128个
- 所有member的长度都小于64字节

其他情况下将使用dic+skiplist实现，具体zset的实现将单独写。

## 总结

- skiplist和各种平衡树（如AVL、红黑树等）的元素是有序排列的，hash表是无序的。因此，在哈希表上只能做单个key的查找，不适宜做范围查找。
- 在做范围查找的时候，平衡树比skiplist操作要复杂。在平衡树上，我们找到指定范围的小值之后，还需要以中序遍历的顺序继续寻找其它不超过大值的节点。直接进行中序遍历是比较麻烦的，而在skiplist上进行范围查找就非常简单，找到最小值之后，顺序对第0层遍历到大于最大值即可。
- 平衡树的插入和删除操作可能引发子树的调整，逻辑复杂，而skiplist的插入和删除只需要修改相邻节点的指针，操作简单又快速。
- 从内存占用上来说，skiplist比平衡树更灵活一些。一般来说，平衡树每个节点包含2个指针（分别指向左右子树），而skiplist每个节点包含的指针数目平均为1/(1-p)，具体取决于参数p的大小。如果像Redis里的实现一样，取p=1/4，那么平均每个节点包含1.33个指针，比平衡树更有优势。
- 查找单个key，skiplist和平衡树的时间复杂度都为O(log n)，大体相当；而哈希表在保持较低的哈希值冲突概率的前提下，查找时间复杂度接近O(1)，性能更高一些。
- 算法难度上skiplist比平衡树简单。