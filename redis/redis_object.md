# redis中的对象系统
相关文件
- object.c
- server.h

## 对象Object

redis中的基础数据结构并不直接提供给指令使用，在这两者之间还有一层对象系统，指令直接操作的是object，object可以由一种或多种数据结构实现。redis中一共有7中对象类型。

```c
#define OBJ_STRING 0    /* String object. */
#define OBJ_LIST 1      /* List object. */
#define OBJ_SET 2       /* Set object. */
#define OBJ_ZSET 3      /* Sorted set object. */
#define OBJ_HASH 4      /* Hash object. */
// 直接由redis module管理
#define OBJ_MODULE 5    /* Module object. */
#define OBJ_STREAM 6    /* Stream object. */
```

同一种对象可能有不同的编码类型

```c
#define OBJ_ENCODING_RAW 0     /* 不使用任何内部结构编码 */
#define OBJ_ENCODING_INT 1     /* 整数 */
#define OBJ_ENCODING_HT 2      /* 编码为hash table */
#define OBJ_ENCODING_ZIPMAP 3  /* 使用zipmap */
#define OBJ_ENCODING_LINKEDLIST 4 /* 已经没了，弃用 */
#define OBJ_ENCODING_ZIPLIST 5 /* 使用ziplist */
#define OBJ_ENCODING_INTSET 6  /* 使用intset */
#define OBJ_ENCODING_SKIPLIST 7  /* 使用skiplist */
#define OBJ_ENCODING_EMBSTR 8  /* 使用sds */
#define OBJ_ENCODING_QUICKLIST 9 /* 使用quicklist */
#define OBJ_ENCODING_STREAM 10 /* 使用radix tree of listpacks */
```
object对象结构体
```c
#define LRU_BITS 24
#define LRU_CLOCK_MAX ((1<<LRU_BITS)-1) /* Max value of obj->lru */
#define LRU_CLOCK_RESOLUTION 1000 /* LRU clock resolution in ms */
typedef struct redisObject {
    unsigned type:4; // 类型
    unsigned encoding:4; // 编码
    // 计时器
    unsigned lru:LRU_BITS; /* LRU time (relative to global lru_clock) or
                            * LFU data (least significant 8 bits frequency
                            * and most significant 16 bits access time). */
    int refcount;
    void *ptr;
} robj;
```

可以看到里面有一个refcount，作为引用计数，这个字段关系到对象何时被释放，通过引用同一个对象来节省内存，为0才会被删除
### 对象创建

申请一块内存用来保存ptr指向的对象
```c
robj *createObject(int type, void *ptr) {
    robj *o = zmalloc(sizeof(*o));
    o->type = type;
    o->encoding = OBJ_ENCODING_RAW;
    o->ptr = ptr;
    o->refcount = 1;
    if (server.maxmemory_policy & MAXMEMORY_FLAG_LFU) {
        o->lru = (LFUGetTimeInMinutes()<<8) | LFU_INIT_VAL; // 根据访问频率
    } else {
        o->lru = LRU_CLOCK();  // 根据最近访问时间进行淘汰，精确到分钟
    }
    return o;
}
```

### 对象编码

不同数据类型调用creat之后，会指定编码
OBJ_STRING: OBJ_ENCODING_INT / OBJ_ENCODING_RAW / OBJ_ENCODING_EMBSTR
OBJ_LIST: OBJ_ENCODING_QUICKLIST（OBJ_ENCODING_ZIPLIST的list实际上没有被用到）
OBJ_SET: OBJ_ENCODING_HT / OBJ_ENCODING_INTSET
OBJ_ZSET: OBJ_ENCODING_SKIPLIST / OBJ_ENCODING_ZIPLIST
OBJ_HASH: OBJ_ENCODING_HT / OBJ_ENCODING_ZIPLIST
OBJ_MODULE: 无
OBJ_STREAM: 无


## redisCommand

```c
struct redisCommand {
    char *name; // 客户端使用的命令的名字
    redisCommandProc *proc; // 命令接口
    int arity;  // 参数数量
    char *sflags;   // 字符串flag，一个字符为一个flag
    uint64_t flags; // sflags的数字表示

    int firstkey; // 参数中第一个key的位置，0表示没有key
    int lastkey;  // 最后一个key的位置
    int keystep;  // 第一个到最后一个key的跨度
    // 分别是这个命令总执行时间以及总调用次数
    long long microseconds, calls; 
    int id;     // 从0开始用来做ACL
};

/* flag字段对应的char
 * w: 写指令
 * r: 读指令
 * m: 会增加内存使用量的指令（如果out of memory了就不要使用）
 * a: 管理员指令（SAVE / SHUTDOWN 等)
 * p: 发布订阅相关指令
 * f: 没有指令用到这个flag
 * s: 不能在脚本中使用的指令
 * R: 带有随机性的指令(SPOP / RANDOMKEY / ···）
 * S: 软指令，会输出数组，有确定性（hkeys/hvals/smembers/sdiff/sunion/sinter/keys），不会与R共存
 * l: 在加载数据库过程中允许使用的指令（select/shutdown/···)
 * t: slave有陈旧的数据，但是不允许使用这些数据，通常这种情况下只有少量指令能用
 * M: 开启了MONITOR时不需要列入监控的指令(exec)
 * k: 和cluster模式有关，只有restore-asking用到
 * F: 高速指令，O(1)或O(log(N))。如果会触发del（del可能会拖时间），那么不是高速指令（如set不是高速指令，而get是）
 */
```

## 数据类型

### t_string
string内部时使用的sds实现，对sds进行包装对外部命令提供接口。
- 大小不会超过512MB
- 二进制安全(因为使用sds)
- t_string.c主要是命令层的逻辑，主要数据结构为sds

```c
#define OBJ_ENCODING_EMBSTR_SIZE_LIMIT 44

// 对于长度小于OBJ_ENCODING_EMBSTR_SIZE_LIMIT使用sds创建string对象
// 头部以及sds的空间申请一次完成
// robj头部16字节，sdshdr8 3字节，加上44字节的数据，再加一个结尾，刚好64字节，满足内存对齐，稳
// 这里的编码为OBJ_ENCODING_EMBSTR
robj *createEmbeddedStringObject(const char *ptr, size_t len)

// 对于不能直接放到一个内存块(64字节)的数据，分成两步申请内存，先申请robj头部，再申请sds
// 因为连续起来没意义了，不满足内存对齐了
// 这里的编码为OBJ_ENCODING_RAW
robj *createRawStringObject(const char *ptr, size_t len)

// string还有一种编码OBJ_ENCODING_INT
// 这种情况下会调用sds中处理数据的转换进行编码
```
命令操作
```c
#define OBJ_SET_NO_FLAGS 0
#define OBJ_SET_NX (1<<0)          /* Set if key not exists. */
#define OBJ_SET_XX (1<<1)          /* Set if key exists. */
#define OBJ_SET_EX (1<<2)          /* Set if time in seconds is given */
#define OBJ_SET_PX (1<<3)          /* Set if time in ms in given */
#define OBJ_SET_KEEPTTL (1<<4)     /* Set and keep the ttl */
// 根据set的不同flag进行包装
// 会根据flag上述的参数来决定如何set，比如先判断key是否存在，每个flag占用一位
// 通过genericSetKey写入k/v，并更新expire，增加val的引用计数
// 通知watch key
void setGenericCommand(client *c, int flags, robj *key, robj *val, robj *expire, int unit, robj *ok_reply, robj *abort_reply)

// 对指定key，value便宜offset之后进行覆盖，会触发扩容
void setrangeCommand(client *c)

// 读取key对应的sds，并进行增加或减少
void incrbyCommand(client *c)
```


编码
```c
// 对于string类型，进行编码来节省内存
robj *tryObjectEncoding(robj *o) {
    long value;
    sds s = o->ptr;
    size_t len;

    // 确保对象类型是string
    serverAssertWithInfo(NULL,o,o->type == OBJ_STRING);

    // 只对RAW和EMBSTR编码的对象进行编码，因为他们他们存储的是string原生的char序列
    if (!sdsEncodedObject(o)) return o;

    // 对共享对象编码不安全
    if (o->refcount > 1) return o;

    // 检查是否能用long整数来表示
    // 超过20个char是没办法用32或64位整数表示的
    len = sdslen(s);
    if (len <= 20 && string2l(s,len,&value)) {
       // 转换为long成功，看看能不能用shared object来表示

       // 如果采用了Maxmemory，那么需要设定不同的LRU用来淘汰，那么不能使用shared object
        if ((server.maxmemory == 0 ||
            !(server.maxmemory_policy & MAXMEMORY_FLAG_NO_SHARED_INTEGERS)) &&
            value >= 0 &&
            value < OBJ_SHARED_INTEGERS)
        {
            decrRefCount(o);
            // 这里预存了10000个小的数字对象用来共享
            incrRefCount(shared.integers[value]);
            return shared.integers[value];
        } else {
            // 如果不能使用共享小数字对象，那就直接用原本指向sds的64位指针来存储数字，机智
            if (o->encoding == OBJ_ENCODING_RAW) {
                sdsfree(o->ptr);
                o->encoding = OBJ_ENCODING_INT;
                o->ptr = (void*) value;
                return o;
            } else if (o->encoding == OBJ_ENCODING_EMBSTR) {
                // 这么小的对象走到这里，肯定是因为不能共享

                // 没办法像上面那样释放指针，因为他们都是一起申请的空间
                // 所以只能-1引用，模仿上面重建一个
                decrRefCount(o);
                return createStringObjectFromLongLongForValue(value);
            }
        }
    }

    // 如果不能转成数字，尝试使用EMBSTR编码
    if (len <= OBJ_ENCODING_EMBSTR_SIZE_LIMIT) {
        robj *emb;

        if (o->encoding == OBJ_ENCODING_EMBSTR) return o;
        emb = createEmbeddedStringObject(s,sdslen(s));
        decrRefCount(o);
        return emb;
    }

    // 对大的string做最后的尝试，看看尾部有没有10%空闲空间，有的话重新分配
    trimStringObjectIfNeeded(o);

    return o;
}
```

解码
```c
robj *getDecodedObject(robj *o) {
    robj *dec;

    if (sdsEncodedObject(o)) {
        incrRefCount(o);
        return o;
    }
    if (o->type == OBJ_STRING && o->encoding == OBJ_ENCODING_INT) {
        char buf[32];

        ll2string(buf,32,(long)o->ptr);
        dec = createStringObject(buf,strlen(buf));
        return dec;
    } else {
        serverPanic("Unknown encoding type");
    }
}
```

解码比较简单，如果是string直接返回，如果是整数，则转成string返回。

### 总结
- 对于incr命令，如果编码为INT直接操作，否则尝试转换
- 对append、setbit、getrange命令，针对的为string，所以需要先将long转为string进行操作

## t_list
redis的list内部只是用了quicklist，使用robj *createQuicklistObject(void) 创建。

关于list，redis配置中有两个相关的属性。
- list-max-ziplist-size：对应于quicklist中的fill表示quicklist中节点的大小(ziplist)，quicklist实际上是作为ziplist的容器。大小有两个指标，entry数量或者ziplist字节总数。
```python
按字节总数：
-5: max size: 64 Kb <-- not recommended for normal workloads
-4: max size: 32 Kb <-- not recommended
-3: max size: 16 Kb <-- probably not recommended
-2: max size: 8 Kb <-- good（默认值）
-1: max size: 4 Kb <-- good

按entry数：
要填大于等于0的数
每个节点最多存这么多个entries
每个节点单独计数
```

- list-compress-depth：表示头尾各有多少个节点不压缩，0表示关闭压缩(默认)

t_list中对list进行一层包装，提供给命令使用，内部使用quicklist中的函数进行操作。里面所有的value都是对sds的封装。

## t_set

### 特点
- 如果sadd第一个元素是整数，那么会创建intset，否则创建set(dict)
- 所有的元素依旧是sds
- 当添加元素不是整数或者元素数量过多时，会从intset升级为set(server.set_max_intset_entries默认大小为512)

主要记一下交并集操作，其他的都很简单

```c
void sunionDiffGenericCommand(client *c, robj **setkeys, int setnum,
                              robj *dstkey, int op) {
    robj **sets = zmalloc(sizeof(robj*)*setnum);
    setTypeIterator *si;
    robj *dstset = NULL;
    sds ele;
    int j, cardinality = 0;
    int diff_algo = 1; // diff有两种算法，实际上根据集合元素数选择最佳算法

    // 初始化sets
    for (j = 0; j < setnum; j++) {
        robj *setobj = dstkey ?
            lookupKeyWrite(c->db,setkeys[j]) :
            lookupKeyRead(c->db,setkeys[j]);
        // 跳过不存在的kv
        if (!setobj) {
            sets[j] = NULL;
            continue;
        }
        // 检查是不是set类型
        if (checkType(c,setobj,OBJ_SET)) {
            zfree(sets);
            return;
        }
        sets[j] = setobj;
    }

    // 用algo_one_work, algo_two_work算出，用哪个DIFF算法比较好，
    // 一个O(N*M)，N=是第一个set的元素数量，M是set数量
    // 一个 O(N)，N=所有set的元素数量之和
    // 实际就是看第一个集合元素数占的比重
    if (op == SET_OP_DIFF && sets[0]) {
        long long algo_one_work = 0, algo_two_work = 0;

        for (j = 0; j < setnum; j++) {
            if (sets[j] == NULL) continue;

            algo_one_work += setTypeSize(sets[0]);
            algo_two_work += setTypeSize(sets[j]);
        }

        algo_one_work /= 2; // 大小除2
        // 除2之后还大的话，说明第一个集合元素数过多
        diff_algo = (algo_one_work <= algo_two_work) ? 1 : 2;

        if (diff_algo == 1 && setnum > 1) {
            // 按照元素数量降序排序，方便尽快找到重复元素
            qsort(sets+1,setnum-1,sizeof(robj*),
                qsortCompareSetsByRevCardinality);
        }
    }

    // 结果集合
    dstset = createIntsetObject();

    // 并直接塞就行，反正不会重复
    if (op == SET_OP_UNION) {
        for (j = 0; j < setnum; j++) {
            if (!sets[j]) continue; /* non existing keys are like empty sets */

            si = setTypeInitIterator(sets[j]);
            while((ele = setTypeNextObject(si)) != NULL) {
                if (setTypeAdd(dstset,ele)) cardinality++;
                sdsfree(ele);
            }
            setTypeReleaseIterator(si);
        }
    } else if (op == SET_OP_DIFF && sets[0] && diff_algo == 1) {
        // diff算法1
        // 遍历第一个集合的元素与其他集合做isMember
        // 只在第一个集合的放到dstset中
        si = setTypeInitIterator(sets[0]);
        while((ele = setTypeNextObject(si)) != NULL) {
            for (j = 1; j < setnum; j++) {
                if (!sets[j]) continue; /* no key is an empty set. */
                if (sets[j] == sets[0]) break; /* same set! */
                if (setTypeIsMember(sets[j],ele)) break;
            }
            if (j == setnum) {
                /* There is no other set with this element. Add it. */
                setTypeAdd(dstset,ele);
                cardinality++;
            }
            sdsfree(ele);
        }
        setTypeReleaseIterator(si);
    } else if (op == SET_OP_DIFF && sets[0] && diff_algo == 2) {
        // diff2
        // 第一个集合的元素全部放到dstset中，然后对其他集合的元素执行remove，成功移除说明重复了，计数-1
        for (j = 0; j < setnum; j++) {
            if (!sets[j]) continue; /* non existing keys are like empty sets */

            si = setTypeInitIterator(sets[j]);
            while((ele = setTypeNextObject(si)) != NULL) {
                if (j == 0) {
                    if (setTypeAdd(dstset,ele)) cardinality++;
                } else {
                    if (setTypeRemove(dstset,ele)) cardinality--;
                }
                sdsfree(ele);
            }
            setTypeReleaseIterator(si);

            /* Exit if result set is empty as any additional removal
             * of elements will have no effect. */
            if (cardinality == 0) break;
        }
    }

    // 得到结果返回
    if (!dstkey) {
        // 不保存
        addReplySetLen(c,cardinality);
        si = setTypeInitIterator(dstset);
        while((ele = setTypeNextObject(si)) != NULL) {
            addReplyBulkCBuffer(c,ele,sdslen(ele));
            sdsfree(ele);
        }
        setTypeReleaseIterator(si);
        server.lazyfree_lazy_server_del ? freeObjAsync(dstset) :
                                          decrRefCount(dstset);
    } else {
        /* If we have a target key where to store the resulting set
         * create this key with the result set inside */
        int deleted = dbDelete(c->db,dstkey);
        if (setTypeSize(dstset) > 0) {
            dbAdd(c->db,dstkey,dstset);
            addReplyLongLong(c,setTypeSize(dstset));
            notifyKeyspaceEvent(NOTIFY_SET,
                op == SET_OP_UNION ? "sunionstore" : "sdiffstore",
                dstkey,c->db->id);
        } else {
            decrRefCount(dstset);
            addReply(c,shared.czero);
            if (deleted)
                notifyKeyspaceEvent(NOTIFY_GENERIC,"del",
                    dstkey,c->db->id);
        }
        signalModifiedKey(c,c->db,dstkey);
        server.dirty++;
    }
    zfree(sets);
}
```

## t_hash

### 特点
- 默认使用OBJ_ENCODING_ZIPLIST编码
- 用一个hashTypeLookupWriteOrCreate查找对象时，未找到会创建一个新的

### 配置相关
- hash-max-ziplist-entries 512：用ziplist最多存512个元素
- hash-max-ziplist-value 64：单个元素超过64字节转成dict

其他操作没什么难的

## t_zset
有序集合
### 特点
- 有序并支持范围查询
- 集合内部使用skiplist以及dict同时保存数据，dict便于快速判断key是否存在
- dict中key保存的是skiplist中的引用
- 会升级也会降级

### 配置相关

升降级相关配置

- zset-max-ziplist-entries 128：用ziplist时，最多存128个元素
- zset-max-ziplist-value 64：单个元素超过64字节时，就得转成hashtable

### 指令相关

```c
// ZPOPMIN, ZPOPMAX, BZPOPMIN and BZPOPMAX最终调用的都是同一个函数，只不过B的，在key不存在时会阻塞
// 根据min还是max取出头部或者尾部的元素
void genericZpopCommand(client *c, robj **keyv, int keyc, int where, int emitkey, robj *countarg)

// ZRANGEBYLEX, ZREVRANGEBYLEX 返回指定成员区间内的成员，按成员字典正序排序/倒序, 分数必须相同
// 由于不确定要返回多少个元素使用setDeferredArrayLen标记
// 由于优先按照分数排序，才是字典序，分数不同的集合中结果不准确
void genericZrangebylexCommand(client *c, int reverse)

// ZRANGEBYSCORE, ZREVRANGEBYSCORE 返回指定分数范围内的元素
// 这个肯定准确，因为优先按照分数排序
void genericZrangebyscoreCommand(client *c, int reverse)

// ZADD与ZINCRBY调用的都是这个函数，并根据flag不同采取不同的行为
// 添加过程中可能会触发ziplist与skiplist的转换
void zaddGenericCommand(client *c, int flags)

// ZCARD其实返回的就是元素个数
void zcardCommand(client *c)

// ZCOUNT统计分数范围内的元素数
void zcountCommand(client *c)

// INSERT与UNION都使用此函数
void zunionInterGenericCommand(client *c, robj *dstkey, int op) 
```

有序集合操作比较简单，主要就是一个跳表结构。

## HyperLogLog

hyperloglog命令的结构体实际上是一个string类型的，但是编码使用HLL_SPARSE标记为一个HLL。

```c
void pfaddCommand(client *c) {
    robj *o = lookupKeyWrite(c->db,c->argv[1]);
    struct hllhdr *hdr;
    int updated = 0, j;

    if (o == NULL) {
        /* Create the key with a string value of the exact length to
         * hold our HLL data structure. sdsnewlen() when NULL is passed
         * is guaranteed to return bytes initialized to zero. */
        o = createHLLObject();
        dbAdd(c->db,c->argv[1],o);
        updated++;
    } else {
        if (isHLLObjectOrReply(c,o) != C_OK) return;
        o = dbUnshareStringValue(c->db,c->argv[1],o);
    }
    /* Perform the low level ADD operation for every element. */
    for (j = 2; j < c->argc; j++) {
        int retval = hllAdd(o, (unsigned char*)c->argv[j]->ptr,
                               sdslen(c->argv[j]->ptr));
        switch(retval) {
        case 1:
            updated++;
            break;
        case -1:
            addReplySds(c,sdsnew(invalid_hll_err));
            return;
        }
    }
    hdr = o->ptr;
    if (updated) {
        signalModifiedKey(c,c->db,c->argv[1]);
        notifyKeyspaceEvent(NOTIFY_STRING,"pfadd",c->argv[1],c->db->id);
        server.dirty++;
        HLL_INVALIDATE_CACHE(hdr);
    }
    addReply(c, updated ? shared.cone : shared.czero);
}
```
添加函数会根据hll采用的是sparse还是dense来调用不同的处理方式



```c
void pfcountCommand(client *c) {
    robj *o;
    struct hllhdr *hdr;
    uint64_t card;

    // 统计多个key时会触发merge
    if (c->argc > 2) {
        uint8_t max[HLL_HDR_SIZE+HLL_REGISTERS], *registers;
        int j;

        /* Compute an HLL with M[i] = MAX(M[i]_j). */
        memset(max,0,sizeof(max));
        hdr = (struct hllhdr*) max;
        hdr->encoding = HLL_RAW; /* Special internal-only encoding. */
        registers = max + HLL_HDR_SIZE;
        for (j = 1; j < c->argc; j++) {
            /* Check type and size. */
            robj *o = lookupKeyRead(c->db,c->argv[j]);
            if (o == NULL) continue; /* Assume empty HLL for non existing var.*/
            if (isHLLObjectOrReply(c,o) != C_OK) return;

            /* Merge with this HLL with our 'max' HLL by setting max[i]
             * to MAX(max[i],hll[i]). */
            if (hllMerge(registers,o) == C_ERR) {
                addReplySds(c,sdsnew(invalid_hll_err));
                return;
            }
        }

        /* Compute cardinality of the resulting set. */
        addReplyLongLong(c,hllCount(hdr,NULL));
        return;
    }

    // 计算并更新cache
    o = lookupKeyWrite(c->db,c->argv[1]);
    if (o == NULL) {
        /* No key? Cardinality is zero since no element was added, otherwise
         * we would have a key as HLLADD creates it as a side effect. */
        addReply(c,shared.czero);
    } else {
        if (isHLLObjectOrReply(c,o) != C_OK) return;
        o = dbUnshareStringValue(c->db,c->argv[1],o);

        /* Check if the cached cardinality is valid. */
        hdr = o->ptr;
        if (HLL_VALID_CACHE(hdr)) {
            /* Just return the cached value. */
            card = (uint64_t)hdr->card[0];
            card |= (uint64_t)hdr->card[1] << 8;
            card |= (uint64_t)hdr->card[2] << 16;
            card |= (uint64_t)hdr->card[3] << 24;
            card |= (uint64_t)hdr->card[4] << 32;
            card |= (uint64_t)hdr->card[5] << 40;
            card |= (uint64_t)hdr->card[6] << 48;
            card |= (uint64_t)hdr->card[7] << 56;
        } else {
            int invalid = 0;
            /* Recompute it and update the cached value. */
            card = hllCount(hdr,&invalid);
            if (invalid) {
                addReplySds(c,sdsnew(invalid_hll_err));
                return;
            }
            hdr->card[0] = card & 0xff;
            hdr->card[1] = (card >> 8) & 0xff;
            hdr->card[2] = (card >> 16) & 0xff;
            hdr->card[3] = (card >> 24) & 0xff;
            hdr->card[4] = (card >> 32) & 0xff;
            hdr->card[5] = (card >> 40) & 0xff;
            hdr->card[6] = (card >> 48) & 0xff;
            hdr->card[7] = (card >> 56) & 0xff;
            /* This is not considered a read-only command even if the
             * data structure is not modified, since the cached value
             * may be modified and given that the HLL is a Redis string
             * we need to propagate the change. */
            signalModifiedKey(c,c->db,c->argv[1]);
            server.dirty++;
        }
        addReplyLongLong(c,card);
    }
}

```
pfcount会统计每一个key的基数


```c
void pfmergeCommand(client *c) {
    uint8_t max[HLL_REGISTERS];
    struct hllhdr *hdr;
    int j;
    int use_dense = 0; /* Use dense representation as target? */

    /* Compute an HLL with M[i] = MAX(M[i]_j).
     * We store the maximum into the max array of registers. We'll write
     * it to the target variable later. */
    memset(max,0,sizeof(max));
    for (j = 1; j < c->argc; j++) {
        /* Check type and size. */
        robj *o = lookupKeyRead(c->db,c->argv[j]);
        if (o == NULL) continue; /* Assume empty HLL for non existing var. */
        if (isHLLObjectOrReply(c,o) != C_OK) return;

        /* If at least one involved HLL is dense, use the dense representation
         * as target ASAP to save time and avoid the conversion step. */
        hdr = o->ptr;
        if (hdr->encoding == HLL_DENSE) use_dense = 1;

        /* Merge with this HLL with our 'max' HLL by setting max[i]
         * to MAX(max[i],hll[i]). */
        if (hllMerge(max,o) == C_ERR) {
            addReplySds(c,sdsnew(invalid_hll_err));
            return;
        }
    }

    /* Create / unshare the destination key's value if needed. */
    robj *o = lookupKeyWrite(c->db,c->argv[1]);
    if (o == NULL) {
        /* Create the key with a string value of the exact length to
         * hold our HLL data structure. sdsnewlen() when NULL is passed
         * is guaranteed to return bytes initialized to zero. */
        o = createHLLObject();
        dbAdd(c->db,c->argv[1],o);
    } else {
        /* If key exists we are sure it's of the right type/size
         * since we checked when merging the different HLLs, so we
         * don't check again. */
        o = dbUnshareStringValue(c->db,c->argv[1],o);
    }

    /* Convert the destination object to dense representation if at least
     * one of the inputs was dense. */
    if (use_dense && hllSparseToDense(o) == C_ERR) {
        addReplySds(c,sdsnew(invalid_hll_err));
        return;
    }

    /* Write the resulting HLL to the destination HLL registers and
     * invalidate the cached value. */
    for (j = 0; j < HLL_REGISTERS; j++) {
        if (max[j] == 0) continue;
        hdr = o->ptr;
        switch(hdr->encoding) {
        case HLL_DENSE: hllDenseSet(hdr->registers,j,max[j]); break;
        case HLL_SPARSE: hllSparseSet(o,j,max[j]); break;
        }
    }
    hdr = o->ptr; /* o->ptr may be different now, as a side effect of
                     last hllSparseSet() call. */
    HLL_INVALIDATE_CACHE(hdr);

    signalModifiedKey(c,c->db,c->argv[1]);
    /* We generate a PFADD event for PFMERGE for semantical simplicity
     * since in theory this is a mass-add of elements. */
    notifyKeyspaceEvent(NOTIFY_STRING,"pfadd",c->argv[1],c->db->id);
    server.dirty++;
    addReply(c,shared.ok);
}
```
调用hllmerge来处理，并将生成的hll写入数据库