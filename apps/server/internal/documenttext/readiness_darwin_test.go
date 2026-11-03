//go:build darwin

package documenttext

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestMacReaderReadinessIsCachedAndOwnsNoRunner(t *testing.T) {
	engine := integrationEngine(t)
	engine.CheckReaders(context.Background())
	checks := engine.Readiness()
	if len(checks) != 2 || checks[0].Status != "ready" || checks[1].Status != "ready" {
		t.Fatalf("actual reader probes failed: %+v", checks)
	}
	engine.config.HelperPath = "/missing/helper"
	for range 3 {
		if engine.Readiness()[0].Status != "ready" {
			t.Fatal("reading a cached report reran a reader")
		}
	}
	entries, err := os.ReadDir(engine.config.JobRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("successful probes retained unnecessary jobs", err)
	}
}

func TestMacReaderDeniesLoopbackUDPAndUnixSockets(t *testing.T) {
	engine := integrationEngine(t)
	runtimeDir := t.TempDir()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	packet, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer packet.Close()
	short, err := os.MkdirTemp("/private/tmp", "camp-document-socket-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(short)
	unix, err := net.Listen("unix", filepath.Join(short, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close()
	script := fmt.Sprintf(`import {connect} from 'node:net'; import {createSocket} from 'node:dgram';
const denied=(options)=>new Promise((resolve,reject)=>{const socket=connect(options);socket.on('connect',()=>{socket.destroy();reject(new Error('network granted'));});socket.on('error',()=>resolve());});
const udp=new Promise((resolve,reject)=>{const socket=createSocket('udp4');socket.on('error',()=>{socket.close();resolve();});socket.send(Buffer.from('denied'),%d,'127.0.0.1',(error)=>{socket.close();error?resolve():reject(new Error('UDP granted'));});});
await Promise.all([denied({host:'127.0.0.1',port:%d}),denied({path:%q}),udp]);
process.stdout.write(JSON.stringify({result:{format:'pdf',sections:[{id:'p1',label:'Page 1',text:'isolated'}],warnings:[]}}));`, packet.LocalAddr().(*net.UDPAddr).Port, listener.Addr().(*net.TCPAddr).Port, filepath.Join(short, "control.sock"))
	if err := os.WriteFile(filepath.Join(runtimeDir, "extract.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.RuntimeDir = runtimeDir
	if _, err := engine.Extract(context.Background(), "fixture.pdf", []byte("%PDF-1.7")); err != nil {
		t.Fatal(err)
	}
}

func TestMacReaderUnknownOwnerSuspendsOnlyThisEngine(t *testing.T) {
	engine := integrationEngine(t)
	if err := os.MkdirAll(engine.config.JobRoot, 0700); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(engine.config.JobRoot, "unrecognized")
	if err := os.WriteFile(unknown, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := engine.Extract(context.Background(), "fixture.docx", []byte("valid admission"))
	requireFailure(t, err, "unavailable")
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "retained" {
		t.Fatal("recovery changed unknown storage")
	}
	if err := integrationEngine(t).Probe(context.Background(), "docx"); err != nil {
		t.Fatal("one engine's unknown owner affected an independent reader", err)
	}
}

func TestMacReaderRejectsRuntimeContainingServerSecrets(t *testing.T) {
	engine := integrationEngine(t)
	server, err := filepath.EvalSymlinks(filepath.Dir(engine.config.JobRoot))
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(server, "private-model-settings")
	if err := os.WriteFile(private, []byte("synthetic-private-server-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`import {readFileSync} from 'node:fs'; process.stdout.write(JSON.stringify({result:{format:'pdf',sections:[{id:'p1',label:'Page 1',text:readFileSync(%q,'utf8')}],warnings:[]}}));`, private)
	if err := os.WriteFile(filepath.Join(server, "extract.mjs"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	engine.config.RuntimeDir = server
	_, err = engine.Extract(context.Background(), "fixture.pdf", []byte("%PDF-1.7"))
	if err == nil {
		t.Fatal("a broad reader runtime exposed private Server data")
	}
	requireFailure(t, err, "unavailable")
	entries, err := os.ReadDir(engine.config.JobRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe runtime started a reader job", err)
	}
}
