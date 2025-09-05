In late august, I accidently lost all the process that I have made in this project :( ....

which I have worked really hard for a few weeks, but I forgot to push to Github.

Now I have to rely on my poor memory for what I have done during that time. I am listing them here as TODOs by go over every file:

1. API schema
   1.1 RetryPolicy need to change to use integers instead of strings
   1.2 RetryPolicy in DbTimer need to use the actual type
   1.3 Use go generator instead of go-gin-server


2. DB
   2.1 remove RangeDeleteWithBatchInsertTxn
   2.2 implment shardId in DB layer
   2.3 fix behavior of RangeGetTimersRequest to be exclusive + inclusive
   2.4 Implement UpdadateTimerByUUID API for updating the next executeAt for nextExecuteAt and backoff retry case
   2.5 Add a flag to indicate the timer callback has failed



3. Engine
   3.1 implement callback processor
   3.2 add local backoff retry
   3.3. implement timer queue

4. Membership
   4.1 reuse what have done in xcherry
