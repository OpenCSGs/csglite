import { t } from "../i18n";
import { publicSessionHeaders } from "../utils/consoleAuth";
import type { DecisionQuestionType } from "./client";
import { buildDecisionRequest } from "../utils/decision";

import type { ServiceTestProtocol } from "./serviceTestProtocols";
export { serviceTestProtocols } from "./serviceTestProtocols";
export type { ServiceTestProtocol } from "./serviceTestProtocols";

export interface ServiceTestOptions { file?: File; question?: string; questionType?: DecisionQuestionType; choices?: string; voice?: string }

async function dataURL(file: File): Promise<string> {
  return new Promise((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result));reader.onerror=()=>reject(reader.error);reader.readAsDataURL(file)});
}
export async function testModelService(model: string, protocol: ServiceTestProtocol, input: string, signal: AbortSignal, options: ServiceTestOptions = {}) {
  const endpoint = `/ai-gateway/v1/${protocol}`;
  let body: BodyInit;
  let jsonBody: Record<string, unknown> | undefined;
  if(protocol==="audio/transcriptions" || protocol==="images/edits") {
    if(!options.file) throw new Error(t("services.testFileRequired"));
    const form=new FormData();form.set("model",model);
    form.set(protocol==="audio/transcriptions"?"file":"image",options.file);
    if(protocol==="images/edits") {form.set("prompt",input);form.set("response_format","b64_json")} else form.set("response_format","json");
    body=form;
  } else {
    jsonBody = protocol === "embeddings" ? {model,input,encoding_format:"float"}
      : protocol === "decisions" ? {...buildDecisionRequest(model,undefined,options.questionType||"predicate",options.question||input,input,options.choices||"")}
      : protocol === "images/generations" ? {model,prompt:input,n:1,response_format:"b64_json"}
      : protocol === "audio/speech" ? {model,input,...(options.voice?.trim()?{voice:options.voice.trim()}:{}),response_format:"wav"}
      : protocol === "responses" ? {model,input,max_output_tokens:256,stream:false}
      : {model,messages:[{role:"user",content:input}],max_tokens:256,stream:false};
    if(options.file && ["chat/completions","responses","messages"].includes(protocol)) {
      const url=await dataURL(options.file);
      if(protocol==="chat/completions") jsonBody.messages=[{role:"user",content:[{type:"text",text:input},{type:"image_url",image_url:{url}}]}];
      if(protocol==="responses") jsonBody.input=[{role:"user",content:[{type:"input_text",text:input},{type:"input_image",image_url:url}]}];
      if(protocol==="messages") jsonBody.messages=[{role:"user",content:[{type:"text",text:input},{type:"image",source:{type:"base64",media_type:options.file.type,data:url.split(",")[1]}}]}];
    }
    body=JSON.stringify(jsonBody);
  }
  const start = performance.now();
  const response = await fetch(endpoint, {
    method: "POST", headers: publicSessionHeaders(endpoint,"POST",{
      ...(jsonBody?{"Content-Type":"application/json"}:{}),
      ...(protocol==="messages"?{"anthropic-version":"2023-06-01"}:{}),
    }),body,signal,
  });
  const durationMS=()=>Math.round(performance.now()-start);
  if(response.ok && protocol==="audio/speech" && !(response.headers.get("content-type")||"").includes("json")) {
    const blob=await response.blob();return {data:null,blob,status:response.status,durationMS:durationMS()};
  }
  const text=await response.text();let data:any;
  try{data=JSON.parse(text)}catch{data={text}}
  if(!response.ok||data.error) {
    const message=typeof data.error==="string"?data.error:data.error?.message;
    throw new Error(`HTTP ${response.status}: ${message||data.text||response.statusText}`);
  }
  return {data,blob:undefined,status:response.status,durationMS:durationMS()};
}
export function serviceTestText(data:any,protocol:ServiceTestProtocol):string {
  if(protocol==="chat/completions") return data.choices?.[0]?.message?.content||JSON.stringify(data,null,2);
  if(protocol==="responses") return data.output_text||data.output?.flatMap((item:any)=>item.content||[]).map((item:any)=>item.text||"").join("\n")||JSON.stringify(data,null,2);
  if(protocol==="messages") return data.content?.map((item:any)=>item.text||"").join("\n")||JSON.stringify(data,null,2);
  if(protocol==="audio/transcriptions") return data.text||JSON.stringify(data,null,2);
  if(protocol==="embeddings")return JSON.stringify(data.data?.map((item:any)=>({index:item.index,dimensions:item.embedding?.length,preview:item.embedding?.slice(0,8)}))||data,null,2);
  return JSON.stringify(data,null,2);
}
