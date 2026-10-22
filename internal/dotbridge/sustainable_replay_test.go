package dotbridge

import (
 "testing"
 "strings"
 "github.com/dotcontrol/dotcontrol/internal/dotqueue"
)

func TestSustainableConsumedProofReplayDoesNotRefreshEvidence(t *testing.T){
 s,_,id:=openTestBridge(t)
 p,err:=s.StartProbe(id);if err!=nil{t.Fatal(err)}
 c,err:=s.ClaimTask(id,p.JobID,"replay-continuous");if err!=nil{t.Fatal(err)}
 result:=dotqueue.Result{Text:strings.TrimPrefix(c.Submission.Messages[0].Content,"Reply with exactly this nonce: ")}
 if _,err=s.CompleteTask(p.JobID,c.Token,result);err!=nil{t.Fatal(err)}
 s.mu.Lock();before:=s.state.Clients[id].proof(dotqueue.ModeAnswer);s.mu.Unlock()
 VerifyTestSubscription(t,s,id)
 if _,err=s.CompleteTask(p.JobID,c.Token,result);err!=nil{t.Fatal(err)}
 s.mu.Lock();after:=s.state.Clients[id].proof(dotqueue.ModeAnswer);s.mu.Unlock()
 if before!=after{t.Fatal("replayed completion altered consumed proof")}
 status,_:=s.StatusForClient(id);if !status.Ready{t.Fatal("real renewal lost consumed capability")}
}
