# redis实现之存储
相关文件
- server.h
- db.c

## 数据结构

```c
typedef struct redisDb {
    dict *dict;                 // 当前数据库的key空间，所有的key都会被放进去
    dict *expires;              // key的超时时间
    dict *blocking_keys;        // 阻塞的key-client对(BLPOP)
    dict *ready_keys;           // 收到PUSH的阻塞的key
    dict *watched_keys;         // WATCHED keys for MULTI/EXEC CAS 
    int id;                     // 当前数据库id，从0开始
    long long avg_ttl;          // 平均ttl，只用作stats
    unsigned long expires_cursor; /* Cursor of the active expire cycle. */
    list *defrag_later;         // 尝试逐步进行碎片整理的key列表
} redisDb;
```

## 实现原理
redis中将不同的key分别存在不同的slot中，一共有16384个hash slot，使用hash的低14位来区分(使用crc16求值，然后对16384取余)，如果key中含有{}，则只有括号内的内容会被hash，这样可以方便将某些key放到固定的节点中(在不触发rehash时)。slot相关的操作，只有在集群模式下才可用。
```c
// 其中val即为对象系统中所创建的各种对象，key会复制，但是val并不会
// 插入之后会对相应的slot的radix树中插入相应的key，val为空
// 对于list、zset、stream会触发signalKeyAsReady
// singal会将没有被唤醒的blockkey加入到server.ready_keys列表中，并加入db->ready_keys字典中来检查多次加入，但并不会删除block
void dbAdd(redisDb *db, robj *key, robj *val)

// 加载RDB文件时使用，key直接是sds，也不会被调用者释放所以不需要复制，也不需要唤醒block，如果key已经存在了，由调用者决定如何操作
int dbAddRDBLoad(redisDb *db, sds key, robj *val)

// 找到对应的key并替换val，不会更新lru以及过期时间，旧的val如果过大会进行异步free防止阻塞
void dbOverwrite(redisDb *db, robj *key, robj *val)

// set key会分开处理key存在以及不存在的情况
// 设置了keepttl则不会更新过期时间，否则从expire字典中移除key(仅针对val存在的key)
// signal会唤醒在等待key的watcher
void genericSetKey(client *c, redisDb *db, robj *key, robj *val, int keepttl, int signal)

// signal中，当key的修改不是由于上下文中的client引起时c为空(例如过期删除)
// 每次修改key以及flush时都会触发
void signalModifiedKey(client *c, redisDb *db, robj *key) {
    // touchkey会从db->watched_keys字典中取出key对应的clients链表
    // 将所有的client的flag标记上CLIENT_DIRTY_CAS
    touchWatchedKey(db,key);
    // 对于缓存key的client发送invalidate消息
    trackingInvalidateKey(c,key);
}

// 随机返回一个没有过期的key，默认重试100次，这个函数会触发expireIfNeeded
// 有可能有一些理论上过期的key但还存在数据库中，原因是从节点不会主动过期，会等待主节点发送删除指令，读从节点时发生
robj *dbRandomKey(redisDb *db)

// 主从节点做的反应是不同的，主节点会触发向AOF以及从节点流发送删除指令
// 对于某些需要重复判断key是否过期的指令会缓存第一次执行的时间进行比较
// 过程中可能会发生数据库切换，此时需要给slave发送两次指令
// 响应event，之后判断大小决定是异步释放还是同步释放空间
int expireIfNeeded(redisDb *db, robj *key)

// 交换两个db的数据阻塞队列是不会交换的
// 交换完成之后检查队列是否该被唤醒
// 集群模式下是不可以交换滴，因为只有一个db0
int dbSwapDatabases(long id1, long id2)


// 此函数用于SETBIT or APPEND这类指令，用来修改非共享状态下的kv
// val的类型必须是string的，并且会先把int解码为string，如果处于共享状态就新建一个obj，覆盖掉；原obj引用-1
robj *dbUnshareStringValue(redisDb *db, robj *key, robj *o)

// 删除命令会先检查key是否过期，然后再判断是否lazy删除，立刻删除掉会触发通知
// del和unlink都调用这个unlink一定是lazy的
void delCommand(client *c)


// 删除整个slot中所有的key，这就体现出前面rax树以及slot中key个数的作用了，返回被删除的key的个数
unsigned int delKeysInSlot(unsigned int hashslot)

// 清空数据库，dbnum为-1表示清空所有，flag字段EMPTYDB_ASYNC异步EMPTYDB_BACKUP仅释放内存，也可以没有
// 清除数据库是会调用callback，在dict的clear中
// 此函数为每一个节点删除底层数据结构的操作，返回删除的key数量
long long emptyDbGeneric(redisDb *dbarray, int dbnum, int flags, void(callback)(void*))

// 清除所有数据
// 先清除自己的数据，杀死RDB saving子进程，并更新aof文件
void flushallCommand(client *c)

// 查找key用于读操作
// 调用此函数时会检查key是否过期，更新最后访问时间，全局的key命中/miss状态会更新，如果启用keyspace通知，keymiss会触发通知
// flag LOOKUP_NOTOUCH为不更新access time，其他忽略
// key过期，但还没被删，也会返回空
robj *lookupKeyReadWithFlags(redisDb *db, robj *key, int flags)

// 查找key
// 找到会更新最后访问时间，前提是flag允许，并且没有正在运行的保存子线程，防止写混乱
robj *lookupKey(redisDb *db, robj *key, int flags)

// 查找key用来写，必然更新访问时间
robj *lookupKeyWriteWithFlags(redisDb *db, robj *key, int flags)

// SCAN命令，会先解析游标为无符号整型，然后调用此函数(SCAN,HSCAN,SSCAN)
// o非空那必须为Hash，Set或者Zset，o为空那就遍历当前数据库的dict
// 首先根据o是否为空决定跳过参数的数量(不为空有一个key)
// 1.首先处理可选参数count，match，type等
// 2.如果元素数量少，遍历整个集合
// 3.根据过滤条件进行筛选
// 4.返回给客户端
void scanGenericCommand(client *c, robj *o, unsigned long cursor)

// 更新slot2key，并将key以及key的长度插入到rax树中，方便快速找到key属于哪一个slot
void slotToKeyUpdateKey(sds key, int add)

// 从XREAD中解出Key
int *xreadGetKeys(struct redisCommand *cmd, robj **argv, int argc, int *numkeys)

// 从ZUNIONSTORE等命令中解出key
int *zunionInterGetKeys(struct redisCommand *cmd, robj **argv, int argc, int *numkeys)
```

## 总结
db为redis中存储数据的主要结构，采用字典存储数据从而保证每个key查找的效率为O(1)，带有过期时间的key单独存到expire字典中，并且为每一个数据库维护因为等待key而阻塞的队列，用于响应事件。内部一共划分为16384个hash slot，在不发生rehash时key对应的slot是固定的。