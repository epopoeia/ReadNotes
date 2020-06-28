过目即忘患者的笔记
 
    仅为个人学习笔记存档，非博客

## 目录
- [redis](#redis)
- [杂项](#malloc)

## redis

### 数据结构以及内存管理
- [redis内存分配](./redis/redis_malloc.md)
- [redis动态字符串](./redis/redis_sds.md)
- [redis双端链表](./redis/redis_adlist.md)
- [redis-quicklist](./redis/redis_quicklist.md)
- [redis字典](./redis/redis_dict.md)
- [redis跳跃表](./redis/redis_zskiplist.md)
- [redis-HyperLogLog算法](./redis/redis_hyperloglog.md)

### 内存编码

- [redis-intset](./redis/redis_intset.md)
- [redis-ziplist](./redis/redis_ziplist.md)
- [redis-zipmap](./redis/redis_zipmap.md)
- [redis基数树](./redis/redis_radixtree.md)


### 数据类型
- [reids中的对象](./redis/redis_object.md)

### 数据库实现

- [redis存储实现](./redis/redis_db.md)
- [redis通知实现](./redis/redis_nofity.md)
- [redis发布订阅](./redis/redis_psub.md)
- [redis持久化-AOF](./redis/redis_aof.md)
- [redis持久化-RDB](./redis/redis_rdb.md)
- [redis事务实现](./redis/redis_multi.md)

### 客户端以及服务器

- [redis事件实现](./redis/redis_ae.md)
- [redis网络层实现](./redis/redis_net.md)

### 集群

- [redis集群模式](./redis/redis_cluster.md)
- [redis备份](./redis/redis_replication.md)
- [redis哨兵](./redis/redis_sentinel.md)


### 杂项
仅做归档，需要时打开瞅瞅，方便回忆
- [内存分配器](./malloc.md)
- [AVL树](./tree/avltree.md)
- [B树及变种](./tree/btree.md)
- [红黑树](./tree/rbtree.md)
- [网络笔记](./net.md)
