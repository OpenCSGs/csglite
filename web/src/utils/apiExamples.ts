export type ApiExampleMode =
  | "chat"
  | "vision"
  | "embedding"
  | "asr"
  | "text-to-image"
  | "image-to-image";

export interface ApiExamples {
  curl: string;
  python: string;
  javascript: string;
}

export function apiExampleModeFromPipelineTag(pipelineTag?: string, isVision?: boolean): ApiExampleMode {
  switch (pipelineTag) {
    case "text-to-image":
      return "text-to-image";
    case "image-to-image":
      return "image-to-image";
    case "automatic-speech-recognition":
      return "asr";
    case "feature-extraction":
    case "sentence-similarity":
    case "text-embedding":
    case "embedding":
      return "embedding";
    case "image-text-to-text":
      return isVision ? "vision" : "chat";
    default:
      return isVision ? "vision" : "chat";
  }
}

export function buildApiExamples(baseUrl: string, model: string, mode: ApiExampleMode): ApiExamples {
  switch (mode) {
    case "asr":
      return {
        curl: `curl ${baseUrl}/v1/audio/transcriptions \\
  -F model="${model}" \\
  -F file="@audio.mp3" \\
  -F response_format="json"`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

with open("audio.mp3", "rb") as audio:
    response = client.audio.transcriptions.create(
        model="${model}",
        file=audio,
        response_format="json"
    )

print(response.text)`,
        javascript: `const form = new FormData();
form.set("model", "${model}");
form.set("file", audioFile);
form.set("response_format", "json");

const response = await fetch("${baseUrl}/v1/audio/transcriptions", {
  method: "POST",
  body: form
});

const data = await response.json();
console.log(data.text);`,
      };
    case "embedding":
      return {
        curl: `curl ${baseUrl}/v1/embeddings \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "input": ["Hello!"],
    "encoding_format": "float"
  }'`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

response = client.embeddings.create(
    model="${model}",
    input=["Hello!"],
    encoding_format="float"
)

print(response.data[0].embedding)`,
        javascript: `const response = await fetch("${baseUrl}/v1/embeddings", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    model: "${model}",
    input: ["Hello!"],
    encoding_format: "float"
  })
});

const data = await response.json();
console.log(data.data[0].embedding);`,
      };
    case "text-to-image":
      return {
        curl: `curl ${baseUrl}/v1/images/generations \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "prompt": "A watercolor cat sitting on a windowsill",
    "size": "1024x1024"
  }'`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

response = client.images.generate(
    model="${model}",
    prompt="A watercolor cat sitting on a windowsill",
    size="1024x1024"
)

print(response.data[0].b64_json)`,
        javascript: `const response = await fetch("${baseUrl}/v1/images/generations", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    model: "${model}",
    prompt: "A watercolor cat sitting on a windowsill",
    size: "1024x1024"
  })
});

const data = await response.json();
console.log(data.data[0].b64_json);`,
      };
    case "image-to-image":
      return {
        curl: `curl ${baseUrl}/v1/images/edits \\
  -F model="${model}" \\
  -F prompt="Turn this into a watercolor illustration" \\
  -F image="@input.png"`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

with open("input.png", "rb") as image:
    response = client.images.edit(
        model="${model}",
        prompt="Turn this into a watercolor illustration",
        image=image,
    )

print(response.data[0].b64_json)`,
        javascript: `const form = new FormData();
form.set("model", "${model}");
form.set("prompt", "Turn this into a watercolor illustration");
form.set("image", imageFile);

const response = await fetch("${baseUrl}/v1/images/edits", {
  method: "POST",
  body: form
});

const data = await response.json();
console.log(data.data[0].b64_json);`,
      };
    case "vision":
      return {
        curl: `curl ${baseUrl}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "messages": [
      {"role": "user", "content": [
        {"type": "text", "text": "What is in this image?"},
        {"type": "image_url", "image_url": {"url": "data:image/png;base64,<BASE64_DATA>"}}
      ]}
    ],
    "stream": true
  }'`,
        python: `import base64
from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

with open("image.png", "rb") as f:
    img_b64 = base64.b64encode(f.read()).decode()

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {
            "role": "user",
            "content": [
                {"type": "text", "text": "What is in this image?"},
                {"type": "image_url", "image_url": {"url": f"data:image/png;base64,{img_b64}"}}
            ]
        }
    ],
    stream=True
)

for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")`,
        javascript: `const imgBase64 = "..."; // Base64-encoded image data

const response = await fetch("${baseUrl}/v1/chat/completions", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    model: "${model}",
    messages: [
      {
        role: "user",
        content: [
          { type: "text", text: "What is in this image?" },
          { type: "image_url", image_url: { url: \`data:image/png;base64,\${imgBase64}\` } }
        ]
      }
    ],
    stream: false
  })
});

const data = await response.json();
console.log(data.choices[0].message.content);`,
      };
    default:
      return {
        curl: `curl ${baseUrl}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ],
    "stream": true
  }'`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="unused"
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "user", "content": "Hello!"}
    ],
    stream=True
)

for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")`,
        javascript: `const response = await fetch("${baseUrl}/v1/chat/completions", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    model: "${model}",
    messages: [
      { role: "user", content: "Hello!" }
    ],
    stream: false
  })
});

const data = await response.json();
console.log(data.choices[0].message.content);`,
      };
  }
}
