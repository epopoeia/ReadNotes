# redis集群模式

## 简介

redis cluster模式主要是为了缓解单机压力，为redis分布式解决方案。

在main函数中会创建集群。
loadServerConfig(configfile,options)载入配置文件。
底层最终调用loadServerConfigFromString()函数，会解析到cluster-开头的集群的相关配置，并且保存到服务器的状态中。
initServer()初始化服务器。
会为服务器设置时间事件的处理函数serverCron()，该函数会每间隔100ms执行一次集群的周期性函数clusterCron()。
之后会执行clusterInit()，来初始化server.cluster，这是一个clusterState类型的结构，保存的是集群的状态信息。
接着在clusterInit()函数中，如果是第一次创建集群节点，会创建一个随机名字的节点并且会生成一个集群专有的配置文件。如果是重启之前的集群节点，会读取第一次创建的集群专有配置文件，创建与之前相同名字的集群节点。
verifyClusterConfigWithData()该函数在载入AOF文件或RDB文件后被调用，用来检查载入的数据是否正确和校验配置是否正确。
aeSetBeforeSleepProc()在进入事件循环之前，为服务器设置每次事件循环之前都要执行的一个函数beforeSleep()，该函数一开始就会执行集群的clusterBeforeSleep()函数。
aeMain()进入事件循环，一开始就会执行之前设置的beforeSleep()函数，之后就等待事件发生，处理就绪的事件。


主要涉及到gossip以及raft算法，数据结构主要用于记录算法信息以及集群拓扑结构，不想复制了。

### 集群逻辑时间函数

```c
void clusterCron(void) {
    dictIterator *di;
    dictEntry *de;
    int update_state = 0;
    int orphaned_masters; // 没有ok的从节点的主节点数量
    int max_slaves; // 单个主节点ok从节点最大数量
    int this_slaves; // 如果自己是slave，我们master的okslave数量
    mstime_t min_pong = 0, now = mstime();
    clusterNode *min_pong_node = NULL;
    static unsigned long long iteration = 0;
    mstime_t handshake_timeout;

    iteration++; // 调用次数，用来做时间控制的

    // 省略部分代码

    // 握手超时
    handshake_timeout = server.cluster_node_timeout;
    if (handshake_timeout < 1000) handshake_timeout = 1000;

    /* Update myself flags. */
    clusterUpdateMyselfFlags();

    // 重新连接离线节点
    di = dictGetSafeIterator(server.cluster->nodes);
    server.cluster->stats_pfail_nodes = 0;
    while((de = dictNext(di)) != NULL) {
        clusterNode *node = dictGetVal(de);

        // 忽略自己以及没有地址的节点
        if (node->flags & (CLUSTER_NODE_MYSELF|CLUSTER_NODE_NOADDR)) continue;

        // pfail节点计数
        if (node->flags & CLUSTER_NODE_PFAIL)
            server.cluster->stats_pfail_nodes++;

        // 握手状态节点超时则删除
        if (nodeInHandshake(node) && now - node->ctime > handshake_timeout) {
            clusterDelNode(node);
            continue;
        }

        if (node->link == NULL) {
            clusterLink *link = createClusterLink(node);
            link->conn = server.tls_cluster ? connCreateTLS() : connCreateSocket();
            connSetPrivateData(link->conn, link);
            if (connConnect(link->conn, node->ip, node->cport, NET_FIRST_BIND_ADDR,
                        clusterLinkConnectHandler) == -1) {
                // 如果ping_sent为0，察觉故障无法执行，因此要设置发送PING的时间，当建立连接后会真正的的发送PING命令
                if (node->ping_sent == 0) node->ping_sent = mstime();
                serverLog(LL_DEBUG, "Unable to connect to "
                    "Cluster Node [%s]:%d -> %s", node->ip,
                    node->cport, server.neterr);

                freeClusterLink(link);
                continue;
            }
            node->link = link;
        }
    }
    dictReleaseIterator(di);

    // 一秒ping一个节点
    if (!(iteration % 10)) {
        int j;

        // 随机找，获取距上次ping时间最长的
        for (j = 0; j < 5; j++) {
            de = dictGetRandomKey(server.cluster->nodes);
            clusterNode *this = dictGetVal(de);

            /* Don't ping nodes disconnected or with a ping currently active. */
            if (this->link == NULL || this->ping_sent != 0) continue;
            if (this->flags & (CLUSTER_NODE_MYSELF|CLUSTER_NODE_HANDSHAKE))
                continue;
            if (min_pong_node == NULL || min_pong > this->pong_received) {
                min_pong_node = this;
                min_pong = this->pong_received;
            }
        }
        if (min_pong_node) {
            serverLog(LL_DEBUG,"Pinging node %.40s", min_pong_node->name);
            clusterSendPing(min_pong_node->link, CLUSTERMSG_TYPE_PING);
        }
    }

    // 1.检查没有从节点的主节点
    // 2.每个主节点正常的从节点数量
    // 3.如果我们是从节点，统计我们的主节点从节点的数量
    orphaned_masters = 0;
    max_slaves = 0;
    this_slaves = 0;
    di = dictGetSafeIterator(server.cluster->nodes);
    while((de = dictNext(di)) != NULL) {
        clusterNode *node = dictGetVal(de);
        now = mstime(); /* Use an updated time at every iteration. */

        if (node->flags &
            (CLUSTER_NODE_MYSELF|CLUSTER_NODE_NOADDR|CLUSTER_NODE_HANDSHAKE))
                continue;

        // 检测没有从节点的主节点，只有当我们是一个没有主测从时使用
        if (nodeIsSlave(myself) && nodeIsMaster(node) && !nodeFailed(node)) {
            int okslaves = clusterCountNonFailingSlaves(node);

            // 主节点，没有可用的从节点，有slot==孤儿
            if (okslaves == 0 && node->numslots > 0 &&
                node->flags & CLUSTER_NODE_MIGRATE_TO)
            {
                orphaned_masters++;
            }
            if (okslaves > max_slaves) max_slaves = okslaves;
            if (nodeIsSlave(myself) && myself->slaveof == node)
                this_slaves = okslaves;
        }

        // 半数时间没收到数据，可能有问题，断掉
        mstime_t ping_delay = now - node->ping_sent;
        mstime_t data_delay = now - node->data_received;
        if (node->link && /* is connected */
            now - node->link->ctime >
            server.cluster_node_timeout && /* was not already reconnected */
            node->ping_sent && /* we already sent a ping */
            node->pong_received < node->ping_sent && /* still waiting pong */
            /* and we are waiting for the pong more than timeout/2 */
            ping_delay > server.cluster_node_timeout/2 &&
            /* and in such interval we are not seeing any traffic at all. */
            data_delay > server.cluster_node_timeout/2)
        {
            /* Disconnect the link, it will be reconnected automatically. */
            freeClusterLink(node->link);
        }

        // 没有ping正在进行，并且收到的pong超过了一半的超时时间，开始发新的ping
        if (node->link &&
            node->ping_sent == 0 &&
            (now - node->pong_received) > server.cluster_node_timeout/2)
        {
            clusterSendPing(node->link, CLUSTERMSG_TYPE_PING);
            continue;
        }

        // 如果我们是master，slave请求手动failover，ping它
        if (server.cluster->mf_end &&
            nodeIsMaster(myself) &&
            server.cluster->mf_slave == node &&
            node->link)
        {
            clusterSendPing(node->link, CLUSTERMSG_TYPE_PING);
            continue;
        }

        // 发了ping了
        if (node->ping_sent == 0) continue;

        /* Check if this node looks unreachable.
         * Note that if we already received the PONG, then node->ping_sent
         * is zero, so can't reach this code at all, so we don't risk of
         * checking for a PONG delay if we didn't sent the PING.
         *
         * We also consider every incoming data as proof of liveness, since
         * our cluster bus link is also used for data: under heavy data
         * load pong delays are possible. */
         // 注释留着了，反正就是看起来离线了，检查一下
        mstime_t node_delay = (ping_delay < data_delay) ? ping_delay :
                                                          data_delay;

        if (node_delay > server.cluster_node_timeout) {
            // 超时了标记pfail了
            if (!(node->flags & (CLUSTER_NODE_PFAIL|CLUSTER_NODE_FAIL))) {
                serverLog(LL_DEBUG,"*** NODE %.40s possibly failing",
                    node->name);
                node->flags |= CLUSTER_NODE_PFAIL;
                update_state = 1;
            }
        }
    }
    dictReleaseIterator(di);

    // 如果我们是slave，并且没开启备份，在我们知道master在线后开启
    if (nodeIsSlave(myself) &&
        server.masterhost == NULL &&
        myself->slaveof &&
        nodeHasAddr(myself->slaveof))
    {
        replicationSetMaster(myself->slaveof->ip, myself->slaveof->port);
    }

    /* Abourt a manual failover if the timeout is reached. */
    manualFailoverCheckTimeout();

    if (nodeIsSlave(myself)) {
        clusterHandleManualFailover();
        if (!(server.cluster_module_flags & CLUSTER_MODULE_FLAG_NO_FAILOVER))
            clusterHandleSlaveFailover();
        /* If there are orphaned slaves, and we are a slave among the masters
         * with the max number of non-failing slaves, consider migrating to
         * the orphaned masters. Note that it does not make sense to try
         * a migration if there is no master with at least *two* working
         * slaves. */

         // 迁移slave节点
        if (orphaned_masters && max_slaves >= 2 && this_slaves == max_slaves)
            clusterHandleSlaveMigration(max_slaves);
    }

    if (update_state || server.cluster->state == CLUSTER_FAIL)
        clusterUpdateState();
}

```




为啥上面gossip会随机选十分之一呢，如果共有N个集群节点，在超时时间node_timeout内，当前节点最少会收到其他任一节点发来的4个心跳包：因节点最长经过node_timeout/2时间，就会其他节点发送一次PING包。节点收到PING包后，会回复PONG包。因此，在node_timeout时间内，当前节点会收到节点A发来的两个PING包，并且会收到节点A发来的，对于我发过去的PING包的回复包，也就是2个PONG包。因此，在下线监测时间node_timeout*2内，会收到其他任一集群节点发来的8个心跳包。因此，当前节点总共可以收到8*N个心跳包，每个心跳包中，包含下线节点信息的概率是1/10，因此，收到下线报告的期望值就是8*N*(1/10)，也就是N*80%，因此，这意味着可以收到大部分节点发来的下线报告。