import { afterEach, describe, expect, it, vi } from "vitest";
vi.hoisted(() => {
 Object.defineProperty(globalThis, "localStorage", { configurable:true, value:{ getItem:()=>null,setItem:()=>undefined } });
});
import { createEmbeddings, streamChat } from "./client";
import { gatewayInferencePath } from "../utils/gateway";
afterEach(()=>vi.unstubAllGlobals());
function environment() {
 vi.stubGlobal("window",{ location:{href:"http://localhost:11435/ai-gateway",origin:"http://localhost:11435"} });
 vi.stubGlobal("document",{cookie:""});
}
function streamingResponse(content:string) {
 const encoded=new TextEncoder().encode(content);
 return new Response(new ReadableStream({start(controller){
  // Split inside both SSE frames and multibyte characters.
  for(let i=0;i<encoded.length;i+=7)controller.enqueue(encoded.slice(i,i+7));
  controller.close();
 }}),{headers:{"Content-Type":"text/event-stream"}});
}
describe("gateway inference testing",()=>{
 it("uses one public service URL and handles split streaming frames without duplicate completion",async()=>{
  environment();
  const fetcher=vi.fn().mockResolvedValue(streamingResponse('data: {"choices":[{"delta":{"content":"你好"}}]}\n\ndata: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n'));
  vi.stubGlobal("fetch",fetcher);
  const token=vi.fn(); const abort=new AbortController();
  await streamChat("served-qwen",[{role:"user",content:"hello"}],{source:"pool:abc",max_tokens:64},token,abort.signal);
  expect(fetcher.mock.calls[0][0]).toBe("/ai-gateway/v1/chat/completions");
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({model:"served-qwen",source:"pool:abc",stream:true,max_tokens:64});
  expect(fetcher.mock.calls[0][1].signal).toBe(abort.signal);
  expect(token.mock.calls).toEqual([["你好",false],["",true]]);
 });
 it("surfaces upstream stream errors and incomplete streams",async()=>{
  environment();vi.stubGlobal("fetch",vi.fn().mockResolvedValue(streamingResponse('data: {"error":{"message":"source unavailable"}}\n\n')));
  await expect(streamChat("m",[],{source:"pool:a"},()=>{})).rejects.toThrow("source unavailable");
  vi.stubGlobal("fetch",vi.fn().mockResolvedValue(streamingResponse('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n')));
  await expect(streamChat("m",[],{source:"pool:a"},()=>{})).rejects.toThrow("before completion");
 });
 it("keeps direct source identity separate from public services",()=>{
  expect(gatewayInferencePath("chat/completions","provider:first")).toBe("/ai-gateway/providers/first/v1/chat/completions");
  expect(gatewayInferencePath("chat/completions","provider:second")).not.toBe(gatewayInferencePath("chat/completions","provider:first"));
  expect(gatewayInferencePath("audio/transcriptions","cloud")).toBe("/ai-gateway/v1/audio/transcriptions");
 });
 it("sends embedding tests to the embedding endpoint rather than chat",async()=>{
  environment();const fetcher=vi.fn().mockResolvedValue(new Response('{"data":[{"embedding":[0.1,0.2]}]}',{headers:{"Content-Type":"application/json"}}));vi.stubGlobal("fetch",fetcher);
  const output=await createEmbeddings("vector","hello","pool:embed");
  expect(fetcher.mock.calls[0][0]).toBe("/ai-gateway/v1/embeddings");
  expect(JSON.parse(fetcher.mock.calls[0][1].body)).toMatchObject({model:"vector",input:"hello",encoding_format:"float"});
  expect(output).toEqual({data:[{embedding:[0.1,0.2]}]});
 });
});
