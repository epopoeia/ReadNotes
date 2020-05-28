# 范型字典

## 基本特点
- 字典基于散列表
- 每个dict有2个dictht，用来实现rehashing，即扩容操作
- rehashing是渐进式的，不是在瞬间做完
- 键冲突的解决方案是chaining，于是每个dictEntry都有一个next指针，用来构成同哈希键链表
- 因为只有next指针，所以chain是单向的，为了加速插入性能，后加入的kv，会插到链表头而不是链表尾
- rehash和chaining的特点，使得rehashing时，新的表项都插入到dt[1]，而dt用chaing没有容量限制，于是rehashing过程总能完成
## 数据结构

```c
// kv
typedef struct dictEntry {
    void *key;
    union {
        void *val;
        uint64_t u64;
        int64_t s64;
        double d;
    } v;
    struct dictEntry *next;
} dictEntry;

typedef struct dictType {
    uint64_t (*hashFunction)(const void *key);
    void *(*keyDup)(void *privdata, const void *key);
    void *(*valDup)(void *privdata, const void *obj);
    int (*keyCompare)(void *privdata, const void *key1, const void *key2);
    void (*keyDestructor)(void *privdata, void *key);
    void (*valDestructor)(void *privdata, void *obj);
} dictType;

typedef struct dictht {
    dictEntry **table; // 表项
    unsigned long size; // table长度
    unsigned long sizemask; // 掩码 = size-1
    unsigned long used; // kv个数
} dictht;

typedef struct dict {
    dictType *type;
    void *privdata;
    dictht ht[2];  // 通常情况只有ht[0]，rehash用到ht[1]
    long rehashidx; /* -1表示没有进行rehash，否则是rehash的索引 */
    unsigned long iterators; /* 目前有多少个迭代器 */
} dict;
```

## 实现原理
hashtable关键部分为rehash，其他的只是基本的数据结构操作，主要说rehash。

扩容以及所容都会触发rehash操作，扩容以及缩容都是通过dictExpand进行的，实际上是_dictNextPower计算新的hashtable的大小，并设置d->ht[1] = n；d->rehashidx = 0;启动rehash程序。

代码里有很多地方会调用dictIsRehashing判断是否在进行rehash。rehash直接函数为int dictRehash(dict *d, int n);n表示本次rehash移动多少个bucket，n*10次空bucket访问，会强制退出，防止阻塞过长时间。

#### 调用dictRehash的地方
_dictRehashStep里调用dictRehash(d,1),dictRehashMilliseconds(d,100)，超时停止迁移，dictRehashMilliseconds只在server.c里调用。

#### 调用dictExpand的地方

- dictResize：把dict缩小到used大小，使得空间利用率尽量靠近1。dictResize并不在dict.c调用，所以是由上层策略决定的，实际上是根据htNeedsResize，即ratio小于0.1时，就会开始收缩。
- dictKeyIndex：每次调用都会执行_dictExpandIfNeeded，如果used已经大于size且两者之比ratio大于5，则会强制调用dictExpand开始扩容(即使设置了不扩容，超过5之后依旧强制执行)

每次调用expand时都会申请一个新的dic
```c
dictht n; /* the new hash table */
n.size = realsize;
n.sizemask = realsize-1;
n.table = zcalloc(realsize*sizeof(dictEntry*));
n.used = 0;
```
并将新的dic赋值给原本dic

```c
    /* Prepare a second hash table for incremental rehashing */
    d->ht[1] = n;
    d->rehashidx = 0;
```

在rehash结束之后，会释放掉缓冲区
```c
    /* Check if we already rehashed the whole table... */
    if (d->ht[0].used == 0) {
        zfree(d->ht[0].table);
        d->ht[0] = d->ht[1];
        _dictReset(&d->ht[1]);
        d->rehashidx = -1;
        return 0;
    }
```
## hash函数
默认的hash函数是siphash
```c
uint64_t siphash(const uint8_t *in, const size_t inlen, const uint8_t *k);
uint64_t siphash_nocase(const uint8_t *in, const size_t inlen, const uint8_t *k);
```
其中的参数k为dict_hash_function_seed

```c
static uint8_t dict_hash_function_seed[16];
```
在服务启动时进行初始化。

## dictscan
dictscan可以用来遍历字典的所有节点，为啥单独拿出来呢，它的实现是很有趣的。
dictscan迭代器工作方式如下：
- 第一次调用函数时使用cursor(v)为0 ，初始化游标位置。
- 当调用函数时，会返回新的游标，并且在下次迭代必须使用新的游标。
- 当游标返回0时，迭代结束

    注意：在开始与结束之间字典里所有的数据都会被访问到，但是可能有的元素会被返回多次。

```c
unsigned long dictScan(dict *d, unsigned long v, dictScanFunction *fn, dictScanBucketFunction *bucketfn, void *privdata);
```
参数中有一个回调函数fn，在遍历每一个元素时，fn的第一个参数为字典中的privatedata，第二个参数为返回的元素。

### 工作原理

主要思想是增加cursor高位。先将cursor进行比特反转，然后增加cursor，然后再进行比特反转。

idx=HASH(key)&(SIZE-1)
这样做的原因是，由于字典在迭代过程中大小可能会发生改变。

假设当前字典size为8，当前游标为010，下一次遍历位置为110，此时发生扩容，变为16。已经迭代过的000，100，010映射到新的bucket中索引为0000，1000，0100，1100，0010，1010。而这些索引刚好是0110之前的索引，所以不需要重复遍历。在扩容时会先检查小的ht，再检查大的ht。

当字典缩容时，16->8，迭代玩0100后下一个为1100，此时缩容为8变成100。在16的情况下已经遍历过的为0000，1000，0100，缩容之后从100开始不会漏掉节点，只有一个重复而已。

如果采用顺序游标遍历，必然会发生重复，并且扩容发生的越晚重复的元素越多，因为之前遍历过的元素扩展之后会映射为高位为0，1两个游标。并且在缩容时，去掉高位之后会导致部分元素漏掉。

限制
- 是完全无状态的，不需要任何额外的内存占用。

同时这种设计方式有如下缺点：

- 同一个节点可能遍历多次，当然在应用层面这很容易解决
- 每次调用 dictScan 都必须返回多个节点，这是由于必须返回都一个 bucket 的所有数据
- 逆向的 cursor 有点难以理解