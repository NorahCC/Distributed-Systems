# MIT-6.824
# 1. Raft vs. Paxos

## 1.1 Theoretical Comparison Between Raft and Paxos

Paxos and Raft are fundamentally designed to solve the same problem: how a group of machines that may fail can agree on a sequence of operations and execute those operations in the same order, thereby building a consistent Replicated State Machine. However, the two protocols take very different design approaches.

Paxos was not originally designed to maintain a log. It solves a more fundamental problem: in an unreliable network, how can multiple nodes eventually decide on the same value? Therefore, the core object in classic Paxos is **a value**, rather than **a log**. Multi-Paxos effectively places an unlimited number of independent Paxos instances side by side. Each slot runs one Paxos instance, and together those slots form a log. Because each slot progresses independently, slot 1, slot 2, and slot 3 can be at completely different stages. Slot 3 being chosen does not imply that slot 2 has also been chosen, so holes can naturally appear in the log. Much of the complexity in a Multi-Paxos implementation revolves around these holes: how to detect them, how to recover them, how to ensure that a new leader does not overwrite values accepted under an old leader, and how to collect accepted values from a majority and continue making progress. This is why `PaxosServer.java` contains substantial logic for recovery, rerunning Phase 1, merging accepted logs, repairing slots, and related tasks.

Raft takes a different approach from the beginning. Its design goal is not to minimize theoretical assumptions, but to make the protocol understandable and implementable by humans. The authors of Raft observed that, although Paxos is mathematically elegant, much of its engineering complexity comes from recovering historical state after leader changes, dealing with log holes, and coordinating multiple parallel Paxos instances. Raft therefore introduces a much stronger leader model and turns the system into a strictly continuous log-replication pipeline. All write requests in Raft must first go through the leader, and the leader replicates them to followers in order. Followers never initiate log writes themselves and never independently decide the value of a particular slot. To preserve log continuity, Raft requires the leader to append entries only, while followers that detect conflicts must delete the conflicting suffix and accept the leader's version. As a result, Raft cannot have a situation where index 3 exists while index 2 is missing.

At the same time, Raft moves much of the complexity into the election phase. A node can become leader only if its log is sufficiently up to date. This means that a newly elected leader naturally contains the latest committed history in the system. After taking over, it does not need to rerun Prepare, as Paxos does, to recover previously accepted values. It can begin working immediately. In effect, Raft trades stricter leader constraints for simpler log management.

The source of safety in the two protocols also reflects different philosophies. Paxos safety is built on **majority intersection**. Any two majority quorums must overlap, so a value that has already been accepted by a majority can later be discovered and inherited by a new leader through the Prepare phase. In other words, Paxos guarantees safety by learning history **after** a leader change.

Raft guarantees safety **before** a leader is elected. Its voting rules require a candidate's log to be at least as up to date as the voter's log. Therefore, once a candidate is elected, it already contains all committed log entries. Paxos follows the pattern:

> Become leader first, then learn history.

Raft follows the pattern:

> Prove that you have the required history first, then become leader.

At the code level, this philosophical difference becomes even more obvious. In a Paxos implementation, the most important concepts are `ballot`, `slot`, `accepted`, `chosen`, `phase1`, `phase2`, and `recovery`. The system repeatedly asks:

> What value should this slot eventually contain?

In a Raft implementation, the most important concepts are `term`, `log`, `nextIndex`, `matchIndex`, and `commitIndex`. The system repeatedly asks:

> How do we replicate the leader's log to all followers?

Paxos focuses on reaching agreement for each individual slot. Raft focuses on maintaining agreement over the entire log. Both ultimately implement the same kind of replicated state machine, but Paxos constructs a log by combining many independent consensus instances, whereas Raft treats the log itself as a first-class object in the protocol.

In one sentence:

**Paxos places complexity in leader changes and history recovery, using a more general and mathematically oriented mechanism to guarantee consistency. Raft places complexity in continuous leader management and log replication, using stronger constraints to achieve a simpler engineering implementation.**

This is also why, after completing the CS7xx0 Paxos project and then reading the MIT 6.5840 Raft implementation, the difference feels so noticeable. Many Raft rules—only one leader, continuous logs, and the requirement that a node with a stale log cannot become leader—can look like restrictions on flexibility. But those restrictions exist precisely to avoid the thousands of lines of recovery logic that appear in a Paxos implementation.

In a sense, Raft can be understood as:

**A version of Multi-Paxos that deliberately gives up some flexibility in exchange for engineering simplicity.**

This is also why many modern production systems, including etcd, Consul, TiKV, RKE2, and the Kubernetes control plane, use Raft rather than directly implementing full Multi-Paxos.

## 1.2 Comparison of the Election Processes

Raft introduces a very important principle: a node is eligible to become the new leader only if its log is at least as up to date as the logs of other nodes. This rule may look like nothing more than a few lines of log-comparison code inside the `RequestVote` RPC, but in practice it carries a major part of the protocol's safety argument. When people first learn Raft, they often assume that committed logs are protected from loss because elections require a majority. In reality, majority voting by itself mainly prevents multiple leaders from being elected in the same term. The rule that prevents committed log entries from being lost is the **log up-to-dateness comparison**.

Suppose there are three nodes: A, B, and C. A is the leader. It successfully replicates a new log entry, `PUT(x=1)`, to A and B, while C does not receive it because of a network problem. A and B now contain the entry, so the majority-replication requirement has been satisfied. Under Raft's definition, the entry can become committed. From that point on, the operation must never be lost.

Now suppose leader A crashes. The system must elect a new leader. Both B and C may start an election, but their logs are different. B contains the committed entry, while C is still at an earlier point in the log. If Raft imposed no restriction, C might happen to start an election earlier than B, obtain a majority, and become the new leader. If that were allowed, C could later append new entries and overwrite the previously committed operation. An operation that the system had already confirmed would disappear, violating replicated-state-machine consistency.

To prevent this, Raft adds an extra voting rule. When a follower receives a `RequestVote` RPC, it does not vote immediately. It first compares the candidate's log with its own. The comparison is not based only on log length. Raft first compares the term of the last log entry. If the candidate's last log term is larger, the candidate is considered more up to date. If the last log terms are equal, Raft compares the last log indexes; the candidate with the larger index has more entries and is considered more up to date. A voter grants its vote only when the candidate's log is at least as up to date as its own.

Returning to the example, when B receives a `RequestVote` request from C, B sees that C is missing the latest entry. C's `LastLogIndex` is smaller than B's, so B refuses to vote for C. In a three-node cluster, C cannot obtain a majority without B's support and therefore cannot become leader. A node with the required log history, such as B, is the one that can successfully win the election.

The key idea is that Raft does not wait until a new leader has been elected and then recover historical logs. Instead, it excludes nodes with insufficient history **before** they can become leader. Raft places this safety mechanism in the election phase. A node's ability to become leader implies that it has the committed history required by the protocol, so after taking over it does not need to ask other nodes:

> What values did you accept in the past?

It can begin replicating new log entries directly because the election process has already constrained which nodes are eligible to lead.

This is why leader changes look so different in Raft and Paxos. In Paxos, a new leader may initially know nothing about previous accepted values. The protocol allows it to obtain a higher ballot, collect promises from a majority, and then use Prepare (Phase 1) to ask acceptors:

> What values have you previously accepted for this slot?

The new leader must learn this history and continue proposing values that are required by the Paxos safety rules. In other words, Paxos is:

> Become leader first, then learn history.

Raft does the opposite. A node with incomplete history is not allowed to become leader. A candidate must first demonstrate:

> My log is at least as up to date as yours.

If it cannot do so, other nodes will not vote for it. Therefore, when a new Raft leader is elected, it already has the committed history needed to continue operating. It does not rerun Paxos-style Prepare, recover accepted values, or perform slot-by-slot recovery. Raft is:

> Prove that you have the history first, then become leader.

When the protocols are compared directly, they reveal two very different ways of solving the same problem: how to ensure that committed data is never lost. Paxos chooses a more general and mathematically oriented approach. It allows a node to take leadership through a higher ballot, but requires the new leader to learn the system's history before safely proposing values. This leads to mechanisms such as Prepare, accepted values, and recovery.

Raft deliberately adds stronger restrictions. Only a node with a sufficiently up-to-date log may become leader, which effectively moves the history-preservation problem into the election phase. This is why a Raft leader can usually continue operating quickly after election, while a Paxos leader may first need to complete Phase 1 and recover the historical information required for safety.

From an implementation perspective, this is also why the two codebases look so different. `PaxosServer` contains substantial logic around P11/P12, `mergeLogsFromPhase1Replies()`, `finishRecoveryPhase1()`, `ensureNoHoleAtSlotNext()`, and related recovery behavior, because a new leader must continuously learn and repair history. A Raft implementation has almost none of this logic. Instead, it relies on log comparison in `RequestVote` and log-consistency checks in `AppendEntries`.

Raft does not avoid historical recovery because history no longer matters. Rather, the rule that **only a node with a sufficiently up-to-date log may become leader** turns historical recovery into an election-eligibility problem. This is one of the most important and elegant engineering choices that distinguishes Raft from Multi-Paxos.

# 3. Understanding the Difference Through Examples

## 3.1 Everything Works Normally and No Failures Occur

Suppose there are three nodes, A, B, and C, jointly maintaining a KV store. A is the current leader. The client first sends a `PUT(x=1)` request.

In Raft, leader A receives the request and immediately appends the operation to the end of its local log, for example at log index 1. At this point, the entry exists on the leader but has not yet taken effect because it has not been replicated to a majority. A therefore sends `AppendEntries` RPCs to B and C and asks them to replicate the entry at index 1. Once B and C have successfully written the entry, A observes that the entry is now stored on a majority of the three-node cluster and advances `commitIndex` to 1. The applier on each node then observes the advancement of `commitIndex` and applies the entry at index 1 to the state machine, actually updating `x` to `1`. Throughout the process, every node's log remains continuous and grows one index at a time. There are no skipped indexes or holes.

In Paxos, the same request follows a different path. After leader A receives `PUT(x=1)`, it does not simply append the operation to the end of a continuously growing leader log. Instead, it first assigns the operation a slot, for example slot 1. The leader then runs Phase 2 for slot 1 and sends P21 messages to the acceptors, asking whether they will accept the proposal that **the value of slot 1 is `PUT(x=1)`**. Once a majority replies with P22 messages, the leader treats slot 1 as chosen and broadcasts a `Decision` message informing the replicas that the final value of slot 1 is `PUT(x=1)`.

When a replica receives the decision, it does not necessarily execute the operation immediately. It first checks whether its `slotNext` is exactly 1. If so, it executes slot 1, updates `x` to `1`, and advances `slotNext` to 2. When additional requests arrive, the system runs separate Paxos instances for slot 2, slot 3, and so on. Although the final effect is the same as in Raft, each slot is independently running its own consensus process.

## 3.2 The Leader Crashes During Replication

Now consider a more interesting case. Suppose leader A has received three requests in sequence: `PUT(x=1)`, `PUT(y=2)`, and `PUT(z=3)`.

In Raft, assume that the first two entries have already been replicated to a majority and committed, while the third entry has only been written to leader A's local log and has not yet been replicated. At that moment, A crashes. B and C begin a new election. Because the election rules constrain which nodes can win based on log freshness, a new leader that wins the election must preserve the committed history. Suppose B becomes the new leader. It does not need to ask other nodes what happened in the past or run a separate accepted-value recovery protocol.

The third, uncommitted entry is different. If it existed only on the old leader A and was never replicated to a majority, it is not protected as a committed entry. A future leader may overwrite that position with a different command. The important point is that Raft does not perform Paxos-style accepted-value recovery after the leader change. Its election and log-matching mechanisms determine which history is preserved and how divergent follower suffixes are repaired.

Paxos behaves differently. Suppose slots 1 and 2 are chosen, while slot 3 has been accepted by A and B but the leader crashes before broadcasting a `Decision`. From the protocol's perspective, slot 3 may already be chosen because a majority accepted the same proposal, even though no replica has yet learned that fact through the normal decision path. When B becomes the new leader, it cannot simply assign a different value to slot 3 because it does not know what may already have been accepted or chosen.

B must run Phase 1, or Prepare, and ask acceptors:

> What value, if any, have you previously accepted for slot 3?

A reports that it accepted `PUT(z=3)`. B also has an accepted record for `PUT(z=3)`. C reports that it accepted nothing. The new leader must follow the Paxos rule for selecting a value from the Phase 1 replies and preserve the required accepted value. It cannot arbitrarily replace slot 3 with another command. After completing Phase 2 again, the slot can be learned as chosen and execution can continue.

The contrast is clear: Paxos places substantial complexity after a leader change. The new leader must learn enough history before it can continue safely. Raft moves much of this complexity into leader eligibility and ordered log replication.

## 3.3 Why Holes Appear

This is one of the largest differences between the two protocols.

Suppose leader A receives three requests: `cmd1`, `cmd2`, and `cmd3`.

In Paxos, the leader assigns them to slot 1, slot 2, and slot 3. Slot 1 progresses smoothly through Phase 2 and becomes chosen. The leader then begins processing slot 2, but while sending P21 messages a network failure occurs. Only A records the proposal; B and C do not receive it. Slot 2 therefore remains accepted on too few nodes and does not become chosen.

Later, the network recovers and the leader continues processing slot 3. Because communication is working again, slot 3 quickly receives a majority of P22 replies and becomes chosen.

The log state is now:

```text
slot1 : CHOSEN
slot2 : ACCEPTED
slot3 : CHOSEN
```

At first glance, slot 3 has completed consensus. However, a replica still cannot execute it. A replicated state machine must execute operations in strict log order. If one node skipped slot 2 and executed slot 3 immediately, replicas could end up in different states.

The replica therefore sees that `slotNext` is still 2 and execution is blocked. The leader can also detect that slot 3 has been decided while slot 2 has not. There is a hole.

The system must start recovery for slot 2 and run Phase 1 to ask acceptors:

> Has anyone previously accepted a value for slot 2?

If an accepted value exists, recovery must preserve the value required by the Paxos rules. If no acceptor reports an accepted value, the leader can propose a `NO-OP` to fill the hole. Only after slot 2 becomes chosen can replicas execute slot 2 and then continue to the already-chosen slot 3.

Raft does not allow this kind of hole to arise in the same way. The leader replicates a continuous log. A follower that does not have the required preceding log prefix will reject an `AppendEntries` request whose `PrevLogIndex` and `PrevLogTerm` do not match. The leader then adjusts `nextIndex`, finds the common prefix, and sends the missing entries. As a result, Raft does not need a Paxos-style mechanism that asks, slot by slot, what value an unresolved hole should contain.

These examples reveal the deepest design difference between the protocols.

Raft is more like a production line. Every item must pass through stage one, stage two, and stage three in order. If an earlier stage is incomplete, later work cannot simply bypass it. The pipeline stays orderly and continuous, but it depends on a strong manager—the leader—to coordinate the process.

Paxos is more like a collection of independent projects. Each slot is its own project. Some may finish early, some may be waiting for approval, and some may be stuck. This provides more flexibility, but it also creates additional complexity: if a project in the middle is unresolved, the system may need to repair that hole before ordered state-machine execution can continue.

## 1.4 Workflow Diagrams and Code Comparison

### 1.4.1 Comparison of the Overall Entry Points

#### Overall Structure of the Go Raft Implementation

Core state of a Raft node:

```text
Raft
├── state: FOLLOWER / CANDIDATE / LEADER
├── currentTerm / votedFor
├── logs[]
├── commitIndex / lastApplied
├── nextIndex[] / matchIndex[]
├── ticker goroutine
├── broadcaster goroutine per peer
└── applier goroutine
```

In the code, the `Raft` struct stores `term`, `vote`, `logs`, `commitIndex`, `lastApplied`, `nextIndex`, and `matchIndex` in one object. An `Entry` is a log entry containing `Term`, `Index`, and `Command`.

Overall flow:

```text
Make()
  ↓
Initialize Raft state
  ↓
Start ticker()
Start applier()
Start broadcaster() for each peer
  ↓
Wait for:
  - election timeout
  - client Start(command)
  - AppendEntries RPC
  - InstallSnapshot RPC
```

#### Overall Structure of the Java Paxos Implementation

`PaxosServer` is more like an all-in-one node:

```text
PaxosServer
├── Proposer/Leader state
│   ├── currentBallot
│   ├── activeLeader
│   ├── phase1Responder
│   └── phase1Replies
│
├── Acceptor state
│   ├── promisedBallot
│   └── log[slot] = accepted/chosen entry
│
├── Learner/Replica state
│   ├── phase2Responder
│   ├── slotNext
│   ├── completedResults
│   └── executeChosenSlots()
│
└── Recovery/GC state
    ├── recoveringSlots
    ├── recoveryReplies
    ├── firstNonClearedSlot
    └── executedUpTo
```

In the code, `slotAttempt` represents the next slot the leader wants to propose, while `slotNext` represents the next slot the local replica needs to execute. The separation between these two variables is an important difference between the Paxos and Raft implementations.

Overall flow:

```text
init()
  ↓
Initialize ballot / leader hint / heartbeat timer
  ↓
server[0] startElection(ballot=1)
  ↓
Enter event-driven processing:
  - handlePaxosRequest
  - handleP11 / handleP12
  - handleP21 / handleP22
  - handleDecision
  - onHeartbeatTimer
```

### 1.4.2 Comparison of Client Request Flows

#### Raft: The Client Sends the Request to the Leader

```text
Client
  ↓
Leader.Start(command)
  ↓
Leader appends to local logs[]
  ↓
broadcastAppendEntries(false)
  ↓
Follower AppendEntries
  ↓
Leader receives success from a majority
  ↓
commitIndex advances
  ↓
applier() applies to the state machine
```

Corresponding Go flow:

```text
Start(command)
  ├── if not LEADER: return false
  ├── append Entry{currentTerm, nextIndex, command}
  ├── broadcastAppendEntries(false)
  └── return index, term, true
```

`Start` allows only the leader to append a log entry. It appends the command to `rf.logs` and triggers replication.

#### Paxos: The Client Broadcasts to All Servers

```text
Client.sendCommand(operation)
  ↓
Wrap as AMOCommand + PaxosRequest
  ↓
Broadcast to all PaxosServer nodes
  ↓
Only the activeLeader processes a fresh proposal
```

The `PaxosClient` does not locate a leader first. It sends the request to all servers, uses `pendingSequenceNum` for request numbering, and relies on a timer for retries.

On the `PaxosServer` side:

```text
handlePaxosRequest(m)
  ├── if completedResults already contains the result: reply directly
  ├── if the mapped slot is already chosen: try to execute/reply
  ├── if !activeLeader: return
  ├── ensureNoHoleAtSlotNext()
  ├── allocateFreshSlot()
  ├── bindRequestToSlot()
  └── beginPhase2Proposal()
```

The key distinction is:

**Raft's request entry point is "append to the next index in a continuous log." Paxos's request entry point is "find a slot for the request and run Phase 2 for that slot."**

Before handling a fresh request, `PaxosServer` also checks `activeLeader` and calls `ensureNoHoleAtSlotNext()` to avoid continuing to propose new work while an earlier execution hole is blocking progress.

### 1.4.3 Normal Replication / Consensus Flow

#### Normal Raft Flow

```text
Leader.Start(cmd)
  ↓
logs = append(logs, Entry{term, index, cmd})
  ↓
broadcastAppendEntries(false)
  ↓
broadcaster(peer)
  ↓
prepareReplicationArgs(peer)
  ↓
AppendEntriesArgs{
    PrevLogIndex,
    PrevLogTerm,
    Entries,
    CommitIndex
}
  ↓
peer.AppendEntries(args)
  ├── check term
  ├── check prevLogIndex / prevLogTerm
  ├── reject on conflict
  ├── append entries if consistent
  └── update follower commitIndex
  ↓
Leader receives reply
  ├── success:
  │     ├── update nextIndex[peer]
  │     ├── update matchIndex[peer]
  │     └── if a majority has matchIndex >= N
  │         and log[N].term == currentTerm:
  │           commitIndex = N
  └── fail:
        └── move nextIndex[peer] backward
```

`AppendEntries` uses `PrevLogIndex` and `PrevLogTerm` to check whether the follower has the same log prefix. If the terms conflict, the follower returns failure and conflict information. The leader then adjusts `nextIndex`. On success, the leader updates `nextIndex` and `matchIndex`, and advances `commitIndex` based on majority replication and Raft's term rule.

In one sentence:

```text
Raft's core loop:
move nextIndex backward or forward
until the follower's log aligns with the leader's log
```

#### Normal Paxos Flow

```text
activeLeader.handlePaxosRequest(cmd)
  ↓
slot = allocateFreshSlot()
  ↓
bindRequestToSlot(slot, cmd, currentBallot)
  ↓
beginPhase2Proposal(slot, cmd, ballot)
  ↓
send P21(ballot, slot, cmd) to acceptors
  ↓
Follower.handleP21
  ├── if ballot < promisedBallot: reject/ignore
  ├── if ballot >= promisedBallot:
  │     ├── promisedBallot = ballot
  │     ├── log[slot] = ACCEPTED(cmd, ballot)
  │     └── reply P22
  ↓
Leader.handleP22
  ├── collect phase2Responder[slot]
  └── if majority reached:
        chooseSlot(slot)
          ↓
        log[slot].status = CHOSEN
          ↓
        broadcast Decision(slot, cmd)
          ↓
        executeChosenSlots()
```

In `PaxosServer`, `handleP21` is the acceptor path for accepting a Phase 2 proposal. `handleP22` is the leader path for collecting acknowledgements. Once a majority is reached, the leader calls `chooseSlot`.

In one sentence:

```text
Paxos's core loop:
collect a majority of P22 responses for each slot
and mark the slot CHOSEN once a majority is reached
```

### 1.4.4 Comparison of Apply / Execute Flows

#### Raft Apply

```text
commitIndex advances
  ↓
applierCond.Signal()
  ↓
applier()
  ├── while lastApplied < commitIndex:
  │     ├── take logs[lastApplied+1 : commitIndex]
  │     ├── send ApplyMsg to applyCh
  │     └── lastApplied = commitIndex
```

Raft application is driven by `commitIndex`. Once a leader or follower knows that an index is committed, `applier()` sends the continuous committed entries to the service.

#### Paxos Execute

```text
A slot becomes CHOSEN
  ↓
executeChosenSlots()
  ↓
while log[slotNext].status == CHOSEN:
    ├── amoApp.execute(command)
    ├── completedResults[key] = result
    ├── if this server is activeLeader: reply to client
    └── slotNext++
```

Paxos execution is driven by `slotNext`. Even if slot 10 is chosen, the replica cannot execute it while slot 2 remains unresolved.

Therefore:

```text
Raft:
commitIndex = 10
means entries 1..10 can be applied continuously
```

```text
Paxos:
slot 10 is CHOSEN
does not mean slots 1..10 can all be executed
slotNext can advance only through a continuous sequence of CHOSEN slots
```

This is why the Paxos implementation needs hole recovery.

### 1.4.5 Leader / Ballot Formation

#### Raft Leader Flow

```text
ticker()
  ↓
if not leader and election timeout expires:
  ↓
startElection()
  ↓
state = CANDIDATE
currentTerm++
votedFor = self
  ↓
send RequestVote
  ↓
receive majority of votes
  ↓
state = LEADER
initialize nextIndex / matchIndex
  ↓
start heartbeat / AppendEntries
```

`ticker()` wakes up on its timing loop. If the node is the leader, it sends heartbeats. If it is not the leader and the election timeout expires, it starts an election.

A Raft leader is elected by votes. During voting, the candidate's log must be sufficiently up to date. Once a leader is elected, it can begin replicating log entries.

#### Paxos Leader Flow

```text
onHeartbeatTimer()
  ↓
too many missed heartbeats
  ↓
startElection(new Ballot(round, self))
  ↓
send P11(ballot)  // Phase 1 Prepare
  ↓
other nodes handleP11
  ├── if ballot >= promisedBallot
  ├── promisedBallot = ballot
  └── reply P12(ballot, acceptedLog)
  ↓
candidate.handleP12
  ├── collect phase1Responder
  ├── receive majority
  ├── activeLeader = true
  ├── mergeLogsFromPhase1Replies()
  └── ensureNoHoleAtSlotNext()
```

In the Paxos code, P11/P12 is not only about establishing leadership. The P12 replies also carry previously accepted log information. In other words, a Paxos leader becomes active after obtaining promises from a majority and must merge accepted history before proposing new values.

The core difference is:

```text
Raft:
prove that your log is sufficiently up to date before becoming leader
```

```text
Paxos:
obtain leadership with a higher ballot, then learn history from a majority
```

### 1.4.6 Log Repair

#### Raft: Repair Followers Through nextIndex

```text
Leader believes follower needs entry nextIndex[i]
  ↓
send AppendEntries(prevLogIndex = nextIndex[i]-1)
  ↓
Follower checks prevLogIndex/prevLogTerm
  ├── mismatch: reply false + conflictIndex
  └── match: append entries
  ↓
Leader receives false
  ↓
move nextIndex[i] backward
  ↓
retry
```

Raft is repairing the answer to this question:

> Where is the common prefix between this follower and the leader?

Once the common prefix is found, the leader repairs the follower's suffix.

#### Paxos: Repair a Slot Through Recovery

```text
executeChosenSlots()
  ↓
discover that slotNext is not CHOSEN
while lastNonEmpty > slotNext
  ↓
ensureNoHoleAtSlotNext()
  ↓
startRecoveryForSlot(slotNext)
  ↓
broadcast P11(currentBallot) again
  ↓
collect P12 acceptedLog
  ↓
finishRecoveryPhase1(slot)
  ├── find the accepted value with the highest ballot for this slot
  ├── if nobody accepted a value, propose NO-OP
  └── beginPhase2Proposal(slot, recoveredValue)
  ↓
slot becomes CHOSEN
  ↓
executeChosenSlots() continues
```

Paxos is repairing the answer to this question:

> What value should this slot contain?

If a value was previously accepted and must be preserved, recovery continues with that value. If no accepted value constrains the slot, the leader can fill it with a `NO-OP`.

### 1.4.7 Full Side-by-Side Path for the Same Request

Suppose the client sends:

```text
PUT x = 1
```

#### Raft Path

```text
Client
  ↓
Leader.Start(PUT x=1)
  ↓
append logs[index=5, term=T]
  ↓
broadcastAppendEntries(false)
  ↓
Follower AppendEntries
  ├── check prevLogIndex / prevLogTerm
  └── append index=5
  ↓
Leader receives success from a majority
  ↓
a majority has matchIndex >= 5
  ↓
commitIndex = 5
  ↓
applier()
  ↓
ApplyMsg{Command=PUT x=1, CommandIndex=5}
  ↓
KV Server executes the operation
```

#### Paxos Path

```text
Client.sendCommand(PUT x=1)
  ↓
broadcast PaxosRequest to all servers
  ↓
activeLeader.handlePaxosRequest
  ↓
slot = allocateFreshSlot()  // for example, slot=5
  ↓
bindRequestToSlot(5, PUT x=1)
  ↓
beginPhase2Proposal(5, PUT x=1)
  ↓
send P21(ballot, slot=5, value=PUT)
  ↓
Acceptor.handleP21
  └── log[5] = ACCEPTED(PUT, ballot)
  ↓
reply P22
  ↓
Leader.handleP22
  ↓
majority of P22
  ↓
chooseSlot(5)
  ↓
broadcast Decision(5, PUT)
  ↓
all nodes handleDecision
  ↓
if slotNext == 5 and all earlier slots are CHOSEN:
      executeChosenSlots()
  ↓
amoApp.execute(PUT)
  ↓
PaxosReply(result)
```

The most important difference is:

**Raft applies an entry after its index becomes committed.**

**Paxos executes a slot after it becomes chosen and all preceding slots are also ready for ordered execution.**

### 1.4.9 The Most Important Mental Model

The main line of the Raft implementation is:

```text
Leader maintains a continuous log
  ↓
replicate the log to followers
  ↓
use matchIndex to determine majority replication
  ↓
advance commitIndex
  ↓
applier applies entries
```

The main line of the Paxos implementation is:

```text
Leader assigns a slot to a request
  ↓
run Phase 2 for each slot
  ↓
after majority acceptance, the slot becomes CHOSEN
  ↓
Decision propagates the chosen result
  ↓
slotNext advances through continuous chosen slots
  ↓
if there is a hole, run recovery
```

In one sentence:

**The Raft code flow revolves around replicating the leader's continuous log. The Paxos code flow revolves around making each slot independently chosen and then executing chosen slots continuously in `slotNext` order.**


# 2. ShardKV

## 2.1 What Go Lab 5 and CS7xx0 Lab 4 Implement

Go Lab 5 is fundamentally about implementing a **Raft-backed sharded KV storage system**. It is not simply a KV server. Instead, the entire key space is divided into multiple shards, and different replica groups are responsible for different shards. The system has two main types of components. The first is `shardctrler`, which maintains the configuration that determines which group is responsible for which shards. The second is `shardkv`, which actually stores data and executes `Get`, `Put`, and `Append` operations.

When a group joins or leaves, or when shards need to move between groups, `shardctrler` generates a new configuration. After `shardkv` detects a configuration change, it uses Raft to order configuration updates, shard migration events, and client operations in the same replicated log. In other words, the central idea of Go Lab 5 is that **everything that changes the state of a shardkv group—whether it is a normal client request, a configuration change, or a shard-data migration—is ordered through Raft and then executed by the state machine in log order**.

The advantage of this design is that many consistency problems are reduced to a single problem: as long as replicas agree on the Raft log order and execute the same log entries deterministically, they will reach the same state.

CS7xx0 Lab 4, by contrast, implements a **Paxos-backed sharded KV storage system**, with cross-shard and cross-group transaction processing added in the later part of the project. It also has the concepts of a shard master and shard stores, and it must also handle shard ownership, configuration changes, data migration, and client requests. However, its underlying consensus mechanism is Paxos rather than Raft.

This means that the system does not naturally begin with a strong leader maintaining one continuous log. Instead, Paxos slots determine where operations are placed. Each slot must become chosen before replicas can execute operations in slot order. Because Paxos slots may be empty, accepted, chosen, or part of a hole, the Java implementation contains more explicit state management: which slots have been chosen, which slots are waiting to execute, which shards are being received, which shards are being sent out, which requests have completed, and which transactions are in the prepare or commit phase.

Once transactions are introduced, CS7xx0 Lab 4 adds Two-Phase Commit. If a transaction involves multiple shards or groups, it cannot simply finish on one shard and stop there. All participants must first prepare and confirm that they can execute the transaction. Only then can they commit together. If one participant fails or refuses, the transaction must abort.

In one sentence:

**Go Lab 5 implements sharded KV storage, configuration changes, and shard migration under Raft ordering. CS7xx0 Lab 4 implements sharded KV storage, configuration changes, shard migration, and cross-shard transactions under Paxos ordering.**

Go Lab 5 is more focused on teaching how a Raft-backed replicated state machine can be extended to support sharding. CS7xx0 Lab 4 is more focused on teaching how a Paxos-backed replicated state machine can preserve correctness during shard migration and transaction processing.

## 2.2 What Scenarios the Two Labs Actually Handle

In simplified terms, Go Lab 5 implements a **dynamically sharded replicated KV store**. CS7xx0 Lab 4 implements a **dynamically sharded replicated KV store plus cross-shard and cross-group transactions**.

The main focus of Go Lab 5 is to place ordinary KV operations, configuration changes, and shard migration into the Raft-backed replicated state machine and give them a consistent order. CS7xx0 Lab 4 handles these same broad categories of problems, but additionally implements an application-level transaction protocol on top of Paxos.

The first system mainly answers:

> Which group should correctly execute an operation for this key right now?

The second system must additionally answer:

> How can multiple groups atomically commit one logical operation?

Consider the most basic example. Suppose the system has ten shards. `group1` is responsible for shards 0 through 4, while `group2` is responsible for shards 5 through 9.

The client executes:

```text
Put("apple", "1")
```

The system first hashes or maps the key to a shard. Suppose `"apple"` belongs to shard 3. The current configuration says that shard 3 belongs to `group1`, so the request is sent to `group1`.

Inside `group1`, there are multiple servers. They use Raft or Paxos to replicate and order the operation, and eventually every replica executes:

```text
Put("apple", "1")
```

at the corresponding ordered position.

Both labs must handle this scenario. The fundamental requirement is that **a client request must execute only at the correct shard owner, while replicas inside that group remain consistent**.

Now consider a configuration-change scenario. Suppose a new `group3` joins the system. To rebalance load, some shards need to move from `group1` and `group2` to `group3`.

In Go Lab 5, `shardctrler` produces a new configuration. For example, configuration 2 may specify that shard 3 now belongs to `group3`. The `shardkv` groups for `group1` and `group3` discover the new configuration through polling or configuration checks.

`group1` must transfer the data for shard 3 out, while `group3` must wait until the shard data has arrived before serving shard 3.

During this period, if a client continues sending requests for `"apple"`, the system must prevent both the old group and the new group from serving the shard at the same time. Otherwise, concurrent writes could occur at two owners.

Go Lab 5 orders configuration updates, shard-transfer-related state changes, and client operations through Raft so that replicas inside each group have a consistent understanding of when they should stop serving a shard and when they are allowed to begin serving it.

CS7xx0 Lab 4 must handle a similar shard-migration process. However, because its underlying consensus layer is Paxos and because the implementation exposes more explicit protocol state, it maintains more visible shard lifecycle states.

For example, a shard may be:

- `SERVING`, meaning the current group is actively serving it.
- `WAITING_IN`, meaning the new configuration assigns the shard to this group, but the shard data has not yet arrived.
- `SENDING_OUT`, meaning the shard should no longer belong to this group, but the group still needs to send its data to the new owner.

This state machine is more protocol-oriented. The Java implementation frequently needs to explicitly ask:

- Which configuration is currently active?
- Can this shard serve requests?
- Did this request arrive at the wrong group?
- Does this migration message belong to an old configuration?
- Has the shard data already been installed?

Go Lab 5 also needs to solve these correctness problems, but many of the ordering relationships are expressed more tightly through the replicated log and shard state transitions.

Now consider failures. Suppose `group1` has three replicas, A, B, and C. A is the current leader, and a client request reaches A. A crashes while the request is being processed.

Go Lab 5 relies on Raft. If the operation has already been replicated to a majority and committed, the new leader preserves it. If it has not been committed, it may be discarded or overwritten according to Raft's log-reconciliation rules. The client retries, and the request eventually enters Raft again.

CS7xx0 Lab 4 relies on Paxos. If a slot has already been accepted by a majority but the chosen result has not yet been learned and propagated normally, a new leader must use Paxos Phase 1 to recover the relevant history. It must determine whether the slot already has an accepted value that constrains future proposals. It cannot simply overwrite the slot.

In other words, failure recovery in Go Lab 5 is centered more around:

```text
leader log replication
commitIndex
nextIndex
```

Failure recovery in CS7xx0 Lab 4 is centered more around:

```text
ballot
accepted value
chosen slot
hole recovery
```

Finally, consider transactions. This is a major area of functionality that CS7xx0 Lab 4 has and the standard Go Lab 5 shardkv does not.

Suppose a transaction needs to perform:

```text
Transfer:
  Account A -100
  Account B +100
```

If A and B belong to the same shard or the same group, the operation is relatively straightforward. But suppose A belongs to shard 2 and B belongs to shard 8, and those shards are owned by different groups.

The system cannot allow one group to successfully subtract the money while another group fails to add it.

The transaction logic in CS7xx0 Lab 4 exists to solve exactly this problem. The coordinator first sends `prepare` requests to all participating shards or groups. Each participant checks whether it can execute the transaction, locks the relevant keys or shards, and records the pending transaction state.

If every participant agrees, the coordinator sends `commit`.

If one participant rejects the transaction or the protocol determines that it cannot continue, the coordinator sends `abort`.

The standard Go Lab 5 `shardkv` does not normally contain this logic because its primary client operations are single-key `Get`, `Put`, and `Append` operations rather than atomic transactions spanning multiple shards.

Therefore, the scenarios can be summarized as follows:

**Go Lab 5 primarily handles a key space distributed across multiple replica groups, dynamic shard reassignment caused by groups joining or leaving, and maintaining linearizable client behavior while shards migrate.**

**CS7xx0 Lab 4 handles shard ownership, configuration changes, data migration, and consistency inside replica groups, while additionally supporting transaction atomicity across shards and groups: operations on multiple shards must either all succeed or all fail.**


## 2.3 Differences Between the Two Design Philosophies

A **Protocol-centric** Java implementation explicitly encodes, inside the server, what distributed process the system is currently going through. A **Log-centric / State-machine-centric** Go implementation instead tries to encode important, deterministic state changes as consensus-log entries, allowing log order and state-machine execution to absorb more of the protocol complexity.

This difference is not caused by one implementation using Java and the other using Go. Nor does Paxos inherently require the first style while Raft inherently requires the second. More precisely, my Java `ShardStoreServer` chooses to make the server responsible for more protocol coordination, while the Go `shardkv` implementation is more inclined to make the replicated log the backbone of the system.

My Java code already uses Paxos to provide ordering within a replica group. Operations such as `NewConfig`, `ShardMove`, `ShardMoveAck`, and ordinary client commands enter local Paxos and are executed in decision-slot order. Technically, it already has the foundation required to build a replicated state machine.

However, the implementation does not compress all complexity into that abstraction. The server layer still explicitly stores a large amount of information describing **how far each protocol has progressed**.

### 2.3.1 The Two Designs Begin by Asking Different Questions

A Protocol-centric design first asks:

> What is the system doing right now?  
> Which stage of the protocol are we currently in?  
> What is allowed to happen next?

Therefore, in the Java server, a shard is not simply described as either "having data" or "not having data." It has an explicit lifecycle:

```text
NOT_OWNED → WAITING_IN → SERVING
```

or:

```text
SERVING → SENDING_OUT → NOT_OWNED
```

These states are themselves part of the system logic.

When the server receives a message, it must inspect the current state and determine whether the message is valid at this stage. For example, when it receives a `ShardMove`, the server does not only check whether the new configuration assigns the shard to this group. It also checks whether the shard is currently in `WAITING_IN`.

Similarly, when it receives a `ShardMoveAck`, it must check whether the shard is still in `SENDING_OUT`.

In addition, collections such as:

```text
installedMoves
appliedAcks
installedConfigs
```

record whether particular protocol actions have already occurred.

In other words, my server stores more than database state. It also stores substantial **protocol history** and **protocol phase information**.

A Log-centric / State-machine-centric design begins with a different question:

> Which events have already been established through consensus?  
> In what order did they occur?  
> After executing those events, what should the deterministic state be?

The focus is not primarily on asking which protocol stage is currently active. Instead, important changes are represented as state-machine commands.

For example, the system can be abstracted into commands such as:

```text
ClientOp
ConfigUpdate
InstallShard
DeleteShard
```

All of these commands enter the consensus layer and are executed in a deterministic order.

The server's main responsibility therefore becomes:

```text
Receive external event
→ submit to consensus
→ wait for decision
→ apply in log order
→ modify state
```

Both systems ultimately need to solve configuration changes, shard migration, and exactly-once behavior. The difference is how they represent complexity.

The Java style prefers to say:

> Shard 3 is currently waiting to be transferred in.

The Go style is more inclined to say:

> The current configuration has become Config 7, but the `InstallShard` event required for Shard 3 under Config 7 has not yet been applied.

The first representation stores an explicit process state. The second stores a set of established facts and derives the system's current capabilities from those facts.

### 2.3.2 Java Stores the Process; Go Tends to Store Results and Order

This is, in my view, the most important difference.

Consider shard migration.

Suppose that under Config 5, Shard 3 moves from Group A to Group B.

The Java implementation thinks about the process approximately like this:

#### Group A

```text
SERVING
→ discover new configuration
→ SENDING_OUT
→ send ShardMove
→ wait for ShardMoveAck
→ receive Ack
→ NOT_OWNED
```

#### Group B

```text
NOT_OWNED
→ discover new configuration
→ WAITING_IN
→ receive ShardMove
→ order it through Paxos
→ install data
→ SERVING
→ return Ack
```

This is a clearly defined protocol flow.

In my Java server, `ShardState`, `MoveKey`, `installedMoves`, `appliedAcks`, and `ShardMoveTimer` all help the server answer:

> How far has our migration protocol progressed?

The Java server is therefore a real **protocol participant**. It knows that it is currently migrating. It knows what it is waiting for. It knows which messages need to be retransmitted and which acknowledgements have already been applied.

The Go-style mental model is closer to:

```text
Config 5 has been applied.
Shard 3 has not yet been installed.
Therefore, this group cannot serve Shard 3.
```

Then later:

```text
InstallShard(Config 5, Shard 3) has been applied.
Therefore, this group can now serve Shard 3.
```

Of course, RPC, retries, and migration coordination are still required. The difference is that RPC-level process state is kept, as much as possible, from becoming the core source of persistent truth.

The semantics of the system are determined primarily by events that have been confirmed through consensus and applied to the state machine.

I would summarize the difference as:

**Java focuses more on transitions. Go focuses more on committed facts.**

Java describes:

> I am moving from A toward B.

The Go style describes:

> A has already become true.  
> Later, B also becomes true in the log.

This distinction has a major impact on code structure.

### 2.3.3 The Java Server Is an Active Coordinator; the Go Server Is More Like a Log Interpreter

My Java `ShardStoreServer` is highly active.

It continuously asks the `ShardMaster`:

> Has the next Config appeared?

After receiving a configuration, it still needs to determine:

> Has the current migration completed?  
> Have current transactions completed?  
> Are we allowed to accept the next Config?

It then decides whether to propose `NewConfig`.

Once the configuration takes effect, the server actively computes:

- Which shards remain owned by this group?
- Which shards need to be sent out?
- Which shards must be received from other groups?

It then changes shard state, actively sends `ShardMove`, installs retry timers, processes acknowledgements, and eventually removes old data.

The system therefore feels like this:

```text
Server
 ├── manages Config protocol
 ├── manages Migration protocol
 ├── manages Client Requests
 ├── manages Deduplication
 ├── manages Retry
 └── invokes Paxos for ordering
```

This is why I describe it as Protocol-centric.

Paxos is infrastructure used by the server. When the server determines that an action needs to be executed consistently across the group, it submits that action to Paxos.

The Go style tends to reverse this relationship:

```text
Consensus Log
       ↓
    Apply Loop
       ↓
State Machine
```

Peripheral goroutines can poll for configurations, send RPCs, and retry failed communication. However, these peripheral activities should not directly define the system's authoritative state.

Instead, they behave more like:

> Discover an event that may need to happen, then attempt to submit that event to the log.

The final system state is determined by the log.

From the perspective of control flow:

**Java: the server drives the system, and Paxos is the transmission.**

**Go: the consensus log is the main axis of the system, and much of the server logic is organized around that axis.**

### 2.3.4 Protocol-Centric Design More Easily Produces a Combinatorial Explosion of States

This is one direct reason why my Java implementation is more complex than the Go-style design.

Suppose a shard has four states:

```text
NOT_OWNED
WAITING_IN
SERVING
SENDING_OUT
```

At the same time, configuration handling may be in one of several situations:

```text
current configuration
waiting for the next configuration
next configuration received but cannot yet be installed
```

Migration messages may also be in different states:

```text
not sent
sent
retrying
installed by receiver
Ack in transit
Ack received
```

Even before considering transactions, these dimensions can produce many combinations.

For example:

> What happens if a `ShardMove` arrives before the Config?

> What happens if the Config has arrived but the old migration has not completed?

> What happens if a duplicate `ShardMove` arrives?

> What happens if a `ShardMoveAck` is duplicated?

> What happens if a `ShardMove` belongs to an old Config?

> What happens if a shard is already `SERVING` and another duplicate Move arrives?

As a result, Protocol-centric code naturally accumulates defensive checks:

```text
Does the config number match?
Is the state WAITING_IN?
Has this move already been installed?
Has this ack already been applied?
Are we waiting for a configuration?
Are we allowed to accept the next configuration?
Have all migrations completed?
```

These checks do not necessarily mean that the code is poorly written. They arise because the design exposes protocol progress as first-class state and therefore must correctly handle combinations of those states.

My Java code follows this pattern. The server uses multiple maps, sets, enums, and timers together to describe the current protocol stage.

A Log-centric design tries to reduce the number of independent state combinations.

Its principle is:

> If an ordering relationship can be expressed by log order, do not build another independent protocol-state mechanism to express the same relationship again.

For example:

```text
slot 101: ConfigUpdate(5)
slot 102: ClientPut(...)
slot 103: InstallShard(5, shard3)
slot 104: ClientGet(...)
```

The apply loop naturally knows that slot 101 happened before slot 103. It also knows the relative order between slots 102 and 104.

Many causal relationships that would otherwise require state variables and callbacks are directly represented by log order.

This is why Log-centric code can sometimes appear as if it is "not doing very much," when in reality the consensus log is carrying a substantial amount of structural complexity.

### 2.3.5 The Two Designs Guarantee Legality Differently

A Protocol-centric Java design frequently asks, at the moment an operation is about to happen:

> Is it legal to do this right now?

As a result, the code contains many guard conditions.

When a client request arrives, the server may need to check:

```text
Does the Config match?
Is the shard SERVING?
Is the request pending?
Has the request already completed?
```

When advancing to a new Config, the server may need to check:

```text
Has migration completed?
Are there active transactions?
```

When a `ShardMove` arrives, the server may need to check:

```text
Does the Config match?
Is this the correct destination group?
Is the current state WAITING_IN?
Has this Move already been installed?
```

This is a typical form of **dynamic legality checking**.

A State-machine-centric design is more inclined to use another approach:

> Control which commands are allowed to enter the state machine, and use log order to make illegal states harder to create.

For example, configurations may advance strictly one at a time:

```text
Config 4
→ Config 5
→ Config 6
```

The state machine therefore accepts only `currentConfig + 1` when applying a configuration update.

An `InstallShard` command carries a config number. If the shard has already been installed or the configuration no longer matches, applying the command can become an idempotent no-op.

The system still performs checks, but the nature of those checks is different.

The Java style asks:

> Which stage of the protocol are we currently in?

The Go style more often asks:

> Is this already-decided command still applicable to the current state machine state?

The second type of check is often more local because it occurs at a unified apply point.

The first type is more likely to be distributed across handlers, timers, callbacks, and protocol transitions.

### 2.3.6 Duplicate-Message Handling Also Reflects the Design Philosophy

A Protocol-centric system often needs to design idempotency mechanisms separately for different protocols.

For example, in the Java implementation:

```text
Client Request
→ RequestKey
```

```text
Shard Move
→ MoveKey + installedMoves
```

```text
Shard Move Ack
→ MoveKey + appliedAcks
```

The transaction layer then introduces additional structures such as:

```text
TxnAttemptKey
CoordinatorTxnState
ParticipantTxnState
```

In other words, every additional protocol may introduce its own:

```text
identity
deduplication
state
retry
completion tracking
```

This pattern is very visible in my code.

A Log-centric system must also handle duplicates, but it tends to move more duplicate handling toward the state-machine execution boundary:

```text
It is acceptable for a command to be proposed more than once.
Consensus determines order.
At apply time, check command identity.
If already executed, treat it as a no-op or return the cached result.
```

This does not mean that Log-centric systems have no deduplication state. The difference is that they tend to establish a smaller number of unified idempotency boundaries.

This is an important engineering difference:

**Protocol-centric idempotency is often per-protocol.**

**Log-centric idempotency tends to be consolidated at the state-machine boundary.**

### 2.3.7 Consensus Has a Different Position in the Two Designs

This point is especially important because it is easy to misinterpret the difference as simply "Raft versus Paxos."

It is not.

My Java code already contains a typical state-machine apply structure. Paxos decisions are stored by slot and then processed continuously beginning at `nextDecisionSlot`. A later slot is not processed before earlier slots have been handled.

This is already the foundation of an ordered replicated state machine.

Therefore, it is entirely possible to build more of the design around this existing structure.

The difference is that my current design is approximately:

```text
Server decides what should happen
        ↓
when consistency is required
        ↓
submit to Paxos
        ↓
Paxos orders the action
        ↓
Server continues the protocol
```

A more purely Log-centric style is:

```text
External world produces an event
        ↓
convert event into Command
        ↓
Consensus Log
        ↓
Unified Apply
        ↓
produce new deterministic state
        ↓
peripheral components continue working based on the new state
```

The arrow order looks only slightly different, but the architectural difference is substantial.

In the first design, the center of control flow is **server protocol logic**.

In the second design, the center of control flow is the **replicated log**.

Therefore, the same Paxos implementation can support a very Log-centric system. Likewise, a Raft-based system can still become extremely complicated if the server accumulates many independent protocol states and timers.

### 2.3.8 Why the Go Style Usually Looks Simpler, but Is Not Necessarily Better in Every Situation

The largest advantage of the Go style is that there are fewer paths through which authoritative state can change.

If the system requires that:

```text
every important state change
must pass through consensus
must enter the ordered log
must be applied by one unified apply loop
```

then many classes of bugs become much harder to create.

For example, it becomes less likely to encounter situations such as:

```text
The leader changed state but followers did not.
An RPC handler and a timer modified the same shard state concurrently.
Ack arrival order caused the state machine to move backward.
Two callbacks had different interpretations of migration completion.
```

The reason is that there is one authoritative path:

```text
Decision → Apply → State Change
```

This is the source of much of the apparent simplicity.

However, Protocol-centric design also has advantages.

One obvious advantage is **protocol observability**.

If I ask:

> Why can Shard 4 not serve requests right now?

The Java implementation can answer directly:

> Because it is in `WAITING_IN`.

If I ask:

> Why does Group A still retain Shard 7?

The answer can be:

> Because it is in `SENDING_OUT` and the Ack has not completed.

This is useful for debugging, tracing, and understanding protocol behavior.

In a more purely Log-centric implementation, answering the same question may require combining:

```text
currentConfig
previousConfig
current shard data
already-applied migration commands
```

and then deriving the current situation.

The two approaches therefore optimize for different things:

**Protocol-centric design optimizes for visibility into the process.**

**Log-centric design optimizes for a unified state-change path.**

### 2.3.9 What Would Change If My Non-Transactional Java Implementation Were Rewritten in a More Go-Style Architecture?

The most obvious change would not be replacing Paxos with Raft.

Instead, the core structure of the server would be redefined.

The current design is approximately:

```text
ShardStoreServer
├── Client Request Protocol
├── Config Protocol
├── Migration Protocol
├── Ack Protocol
├── Retry Protocol
└── Paxos
```

A design closer to the Go style would look more like:

```text
ShardStoreServer
├── Command Proposal Layer
├── Paxos Ordered Log
├── Unified Apply State Machine
└── Background Workers
    ├── Config Poller
    └── Migration RPC Worker
```

The key point is:

**Background workers would no longer own the business truth of the system.**

The Config Poller would only discover:

```text
Config 5 now exists.
```

Then it would propose:

```text
ApplyConfig(5)
```

The Migration Worker would only discover:

```text
The data for Shard 3 has arrived.
```

Then it would propose:

```text
InstallShard(5, 3, data)
```

The client handler would only do:

```text
receive Put(x)
→ propose ClientOp
→ wait for decision
```

The places that actually modify:

```text
currentConfig
KV data
dedup table
shard ownership
```

would be concentrated, as much as possible, in the apply state machine.

With this architecture, my Java implementation could continue using Paxos while becoming structurally much closer to MIT's Go `shardkv`.

### 2.3.10 Summary of the Two Designs

If transactions are removed completely and we discuss only ShardKV, configuration changes, and shard migration, the difference between my Java implementation and the MIT Go implementation can be understood as follows.

My Java implementation treats a distributed system as a collection of cooperating protocols.

Config is a protocol.

Shard Migration is a protocol.

Ack is a protocol.

Retry is a protocol.

The server explicitly records how far each protocol has progressed and decides the next action according to the current protocol state. As a result, the code naturally contains many state enums, pending sets, installed sets, acknowledgement sets, timers, and legality checks.

The core object in this design is:

> A protocol instance that is currently running.

The MIT Go style is more inclined to treat the distributed system as a deterministic state machine driven by a consensus log.

Configuration changes, client operations, and shard installation are, as much as possible, converted into events in the log. The log establishes a global order among events, and the apply loop interprets that order as new state.

Peripheral RPCs and timers still exist, but their role is reduced. They help drive events forward, but they do not directly define the final truth of the system.

The core object in this design is:

> A state transition that has already been established through consensus.

Therefore, when I previously said:

**Java is Protocol-centric, while MIT is Log-centric / State-machine-centric,**

I did not mean that Java has no log, nor that MIT has no protocols.

The point is that the two designs place the center of system complexity in different locations.

My Java code assumes:

> If I describe protocol stages clearly and correctly process the messages, acknowledgements, retries, and state transitions of each stage, the system will eventually behave correctly.

The MIT Go style places more trust in another principle:

> If every important change enters the same ordered log and every replica executes those changes deterministically in the same order, much of the protocol complexity can be absorbed by ordering itself.

Therefore, even after removing transactions, I still believe my Java implementation could be simplified further. This simplification would not require replacing Paxos with Raft.

The real refactoring direction would be to:

- reduce explicit recording of process state inside the server,
- model more actions as Paxos commands,
- elevate the Paxos decision sequence into the true backbone of the system, and
- make configuration changes, migration, and client operations modify state primarily through a unified apply state machine.

In reality, the Java code already sits somewhere between the two design styles rather than being purely Protocol-centric.

`NewConfig`, `ShardMove`, `ShardMoveAck`, and client commands are already ordered through Paxos. `handlePaxosDecision` already processes commands in slot order through `process(command)`.

In other words, the implementation already has a **Log-centric skeleton**. The main difference is that a relatively thick **protocol-state layer** still surrounds that skeleton.


## 2.4 Additional Transaction Processing Logic in CS7xx0

The additional logic in the Java transaction implementation is fundamentally another layer built on top of ordinary ShardKV:

**a distributed transaction protocol across groups.**

Ordinary ShardKV only needs to guarantee the following:

> A key belongs to a shard, a shard belongs to a group, a request is sent to the correct group, and that group uses Paxos or Raft to order and execute the operation consistently.

Transactions must solve a more difficult problem:

> A single operation may read and write multiple keys, and those keys may be distributed across multiple shards and multiple groups. The system must guarantee that all of these reads and writes either take effect together or do not take effect at all.

This is why the Java implementation contains a large amount of logic that the Go `shardkv` implementation does not have.

### 2.4.1 First Additional Layer: Identifying Which Groups Participate in a Transaction

For an ordinary `Put` or `Get`, the system only needs to calculate:

```text
key → shard → group
```

For a transaction, it must calculate:

```text
txn.keySet()
    ↓
which shard each key belongs to
    ↓
which group owns each shard
    ↓
which participant groups are involved in this transaction
```

This is why the Java implementation needs logic similar to:

```text
ownerGroupsForTransaction
localTxnShards
localOwnedTxnShardsServing
```

This logic is not executing KV operations. It is answering questions such as:

> Which groups does this transaction actually span?

> Is the current group one of the participants?

> Is the current group the coordinator?

> Under the current configuration, are all relevant shards able to serve requests?

This layer does not exist in ordinary `shardkv`.

### 2.4.2 Second Additional Layer: Selecting a Coordinator

Ordinary `shardkv` has no transaction coordinator.

A client request is sent to the group that owns the relevant shard, and that group handles the request itself.

A transaction may instead involve:

```text
Group 1: key a
Group 2: key b
Group 3: key c
```

The three groups cannot commit independently whenever they want. Otherwise, the system could reach a state such as:

```text
Group 1 committed
Group 2 failed
Group 3 does not know what happened
```

The Java implementation therefore needs to select a coordinator group.

In my implementation, the group with the largest `groupId` in `ownerGroups` is selected as the coordinator.

When a request arrives, the server therefore needs to determine:

> Am I the coordinator for this transaction?

If not, it returns `wrongGroup` or otherwise causes the client to locate the correct coordinator.

This logic does not exist in ordinary `shardkv`.

### 2.4.3 Third Additional Layer: The 2PC State Machine

The office-hour discussion explicitly explains that a transaction is a **logical unit of work** that must behave as an indivisible action:

> Either the entire transaction completes, or none of it has an effect.

One common way to achieve this is Two-Phase Commit.

My Java implementation uses 2PC.

Compared with ordinary `shardkv`, it therefore introduces stages such as:

```text
TxnStart
    ↓
Prepare
    ↓
PrepareReply
    ↓
Commit / Abort
    ↓
CommitAck / AbortAck
    ↓
Client Reply
```

An ordinary `Put`, `Get`, or `Append` follows a much simpler path:

```text
ClientOp
    ↓
Paxos
    ↓
Apply
    ↓
Reply
```

A transaction does not finish after one log entry. It is a cross-group protocol.

### 2.4.4 Fourth Additional Layer: CoordinatorTxnState

The coordinator must remember how far the entire transaction has progressed.

This is why my Java implementation contains:

```text
CoordinatorTxnState
```

It needs to store information such as:

```text
the transaction itself
configNum
clientReplyAddress
participantGroups
readyGroups
collectedValues
phase
result
finalValues
```

The meaning of this state is that the coordinator first sends `prepare` requests and then waits for all participants to reply that they are ready.

Each participant returns the current values of the keys for which it is responsible.

Once the coordinator has collected all required values, it runs the transaction logic using those values, computes the transaction result, and determines the final values that need to be written.

It then sends `commit`.

Therefore, the coordinator is not simply forwarding messages. It is responsible for:

```text
collecting the read set
computing the transaction result
deciding commit or abort
broadcasting the final decision
waiting for acknowledgements
replying to the client
```

Ordinary `shardkv` has no equivalent role.

### 2.4.5 Fifth Additional Layer: ParticipantTxnState

Each participating group must also store its own transaction state.

The Java implementation therefore contains:

```text
ParticipantTxnState
```

It needs to remember information such as:

```text
whether the transaction has been prepared
whether prepare succeeded
whether it has been committed
whether it has been aborted
the values read during the prepare phase
```

This state is necessary for handling duplicate `prepare`, `commit`, and `abort` messages.

For example, the coordinator may retransmit a `prepare` message because of network loss or timeout.

The participant cannot acquire the same locks again, reread state without considering the previous attempt, and recreate the transaction state from scratch every time. Doing so could violate idempotency.

The participant therefore needs to be able to say:

> I have already prepared this transaction attempt. I will return the previous prepare reply.

This is another layer of idempotency that transaction processing adds.

### 2.4.6 Sixth Additional Layer: Locks

The office-hour discussion also emphasizes that Paxos itself is not a transaction protocol.

Transactions are implemented at the application level. Paxos ensures replicated ordering within a shard group, but application-level locking is still required.

The Java implementation therefore adds structures and logic such as:

```text
shardLocks
canLockShards
lockShards
unlockShards
```

Why are locks necessary?

After a transaction successfully prepares, and before it commits or aborts, the participant must ensure that the relevant shards are not modified by conflicting transactions.

Otherwise, the following could happen:

```text
Txn1 reads x = 1 during prepare
Txn2 immediately changes x to 2
Txn1 commits while still reasoning from x = 1
```

Therefore, after prepare succeeds, the participant locks the relevant shards until commit or abort releases those locks.

Ordinary `shardkv` does not need this transaction-level locking mechanism because individual key operations can simply execute serially according to the Paxos or Raft log.

### 2.4.7 Seventh Additional Layer: Binding Transactions to a Configuration

Transactions introduce another difficult problem:

> The configuration in which the transaction begins and the configurations used by the participating groups during execution must remain compatible.

The office-hour abort case discusses this situation. A transaction may involve several groups, but if one group has already moved into a different configuration, it cannot safely continue participating in the same transaction under the old ownership assumptions. The transaction must abort.

This is why transaction messages in my Java implementation carry:

```text
configNum
```

During `prepare`, `commit`, and `abort`, the server checks whether:

```text
currentConfig == transaction.configNum
```

If the configurations do not match, the transaction cannot continue normally.

This is also why:

```text
canAcceptNewConfig()
```

needs to consider:

```text
hasActiveTransactionWork()
```

If a transaction is currently in the `prepare` or `commit` process, arbitrarily switching configurations could make the transaction cross two different shard-ownership worlds, making correctness much harder to preserve.

### 2.4.8 Eighth Additional Layer: attemptId

Ordinary at-most-once deduplication can generally use:

```text
clientId + sequenceNum
```

Transactions introduce the additional concept of retries and protocol attempts.

The same logical client request may be resent because of a timeout and may even be associated with a new transaction attempt.

This is why my Java implementation distinguishes between:

```text
RequestKey(client, seq)
```

and:

```text
TxnAttemptKey(client, seq, attemptId)
```

This distinction is important.

`RequestKey` represents one logical request from the client's perspective.

`TxnAttemptKey` represents one transaction attempt at the server protocol level.

Transaction deduplication is therefore more complex than ordinary KV deduplication because it must guarantee both:

> The same logical client request ultimately produces only one final result.

and:

> `prepare`, `commit`, and `abort` messages for the same transaction attempt can be retried safely.

### 2.4.9 Ninth Additional Layer: Retry Timers

In ordinary `shardkv`, retries mainly come from client retries or shard-migration retries.

In the transaction implementation, the coordinator must also actively retry protocol messages.

The Java implementation therefore contains timers such as:

```text
PrepareRetryTimer
CommitRetryTimer
AbortRetryTimer
```

Why are these necessary?

After the coordinator sends `prepare`, one participant may fail to reply.

After the coordinator sends `commit`, a participant may successfully commit but its acknowledgement may be lost.

After the coordinator sends `abort`, the abort acknowledgement may also be lost.

The coordinator therefore needs to retransmit messages according to the current phase:

```text
PREPARING → resend prepare
COMMITTING → resend commit
ABORTING → resend abort
```

This is the source of much of the timer logic and phase checking in the Java transaction implementation.

### 2.4.10 Tenth Additional Layer: Collecting Commit/Abort Acknowledgements

For an ordinary `Put` or `Get`, once the operation has executed in the local replica group, that group can reply to the client.

A transaction cannot do this.

The coordinator cannot reply to the client simply because the coordinator itself has committed.

It needs confirmation from all participant groups that they have completed the final phase:

```text
I have committed.
```

or:

```text
I have aborted.
```

The Java implementation therefore contains collections such as:

```text
commitAckedGroups
abortAckedGroups
```

These sets record:

> Which groups have completed Phase 2?

Only after all required groups have acknowledged the decision can the coordinator call logic such as:

```text
finishCommittedCoordinatorTxn
```

or:

```text
finishAbortedCoordinatorTxn
```

and then reply to the client.

### 2.4.11 Fundamental Difference from Ordinary ShardKV

Ordinary `shardkv` solves this problem:

> Execute one operation in order within one group.

Transaction processing solves this problem:

> Atomically execute one logical operation across multiple groups.

Therefore, the additional code is not a small extension. It is an entire protocol layer:

```text
Coordinator selection
Participant discovery
Prepare phase
Shard locks
Read value collection
Transaction execution
Commit/Abort decision
Commit/Abort broadcast
Ack collection
Retry timer
Attempt-level deduplication
Config consistency check
```

### Final Summary

The core of the additional Java transaction logic is:

**On top of Paxos, the implementation uses application-level Two-Phase Commit to provide atomic transactions across shards and groups.**

Paxos can guarantee:

> Replicas inside one group execute commands in the same order.

But Paxos does not automatically guarantee:

> Group 1, Group 2, and Group 3 either all commit together or all give up together.

Therefore, the Java implementation must additionally implement:

```text
coordinator
participant
prepare
commit
abort
locking
retry
acknowledgements
attemptId
configuration checks
```

This is why the transaction portion looks substantially larger than the Go `shardkv` implementation.

The difference is not simply that Java is more verbose.

The Java implementation is solving a problem that the standard Go `shardkv` implementation does not solve.
