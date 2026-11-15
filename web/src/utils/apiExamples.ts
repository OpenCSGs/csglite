export type ApiExampleMode =
  | "chat"
  | "vision"
  | "embedding"
  | "asr"
  | "decision"
  | "tts"
  | "text-to-image"
  | "image-to-image";

export interface ApiExamples {
  curl: string;
  python: string;
  javascript: string;
}

export function apiExampleModeFromPipelineTag(pipelineTag?: string, isVision?: boolean): ApiExampleMode {
  switch (pipelineTag) {
    case "typed-decisions":
    case "decision":return "decision";
    case "text-to-speech":
    case "text-to-audio":return "tts";
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

function buildExamples(baseUrl: string, model: string, mode: ApiExampleMode): ApiExamples {
  switch (mode) {
    case "tts":
      return {
        curl: `curl ${baseUrl}/v1/audio/speech \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${model}","input":"Hello","response_format":"wav"}' \\
  --output speech.wav`,
        python: `from openai import OpenAI

client = OpenAI(base_url="${baseUrl}/v1", api_key="YOUR_API_KEY")
with client.audio.speech.with_streaming_response.create(
    model="${model}", input="Hello", voice="", response_format="wav"
) as response:
    response.stream_to_file("speech.wav")`,
        javascript: `const response = await fetch("${baseUrl}/v1/audio/speech", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ model: "${model}", input: "Hello", response_format: "wav" })
});
const audio = await response.blob();`,
      };

    case "asr":
      return {
        curl: `curl ${baseUrl}/v1/audio/transcriptions \\
  -F model="${model}" \\
  -F file="@audio.mp3" \\
  -F response_format="json"`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="YOUR_API_KEY"
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
    case "decision":
      return {
        curl: `curl ${baseUrl}/v1/decisions \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${model}",
    "input": "I was charged twice for my order.",
    "questions": [{
      "type": "choice",
      "name": "department",
      "instructions": "Which department should handle this complaint?",
      "choices": [
        {"value": "billing", "description": "Payments, invoices, and refunds."},
        {"value": "technical", "description": "Problems using the product."},
        {"value": "shipping", "description": "Delivery and tracking."},
        {"value": "other", "description": "Requests outside these categories."}
      ]
    }]
  }'`,
        python: `from openai import OpenAI

client = OpenAI(
    base_url="${baseUrl}/v1",
    api_key="YOUR_API_KEY"
)

decision = client.decisions.create(
    model="${model}",
    input="I was charged twice for my order.",
    questions=[
        {
            "type": "choice",
            "name": "department",
            "instructions": "Which department should handle this complaint?",
            "choices": [
                {"value": "billing", "description": "Payments, invoices, and refunds."},
                {"value": "technical", "description": "Problems using the product."},
                {"value": "shipping", "description": "Delivery and tracking."},
                {"value": "other", "description": "Requests outside these categories."},
            ],
        }
    ],
)

answer = decision.answers[0]
print(f"Department: {answer.choice} (confidence: {answer.confidence})")`,
        javascript: `import OpenAI from "openai";

const client = new OpenAI({ baseURL: "${baseUrl}/v1", apiKey: "YOUR_API_KEY" });
const decision = await client.decisions.create({
  model: "${model}",
  input: "I was charged twice for my order.",
  questions: [
    {
      type: "choice",
      name: "department",
      instructions: "Which department should handle this complaint?",
      choices: [
        { value: "billing", description: "Payments, invoices, and refunds." },
        { value: "technical", description: "Problems using the product." },
        { value: "shipping", description: "Delivery and tracking." },
        { value: "other", description: "Requests outside these categories." },
      ],
    },
  ],
});

const answer = decision.answers[0];
console.log(\`Department: \${answer.choice} (confidence: \${answer.confidence})\`);`,
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
    api_key="YOUR_API_KEY"
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
    api_key="YOUR_API_KEY"
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
    api_key="YOUR_API_KEY"
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
    api_key="YOUR_API_KEY"
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
    if not chunk.choices:
        continue
    delta = chunk.choices[0].delta
    if delta and delta.content:
        print(delta.content, end="", flush=True)`,
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
    api_key="YOUR_API_KEY"
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "user", "content": "Hello!"}
    ],
    stream=True
)

for chunk in response:
    if not chunk.choices:
        continue
    delta = chunk.choices[0].delta
    if delta and delta.content:
        print(delta.content, end="", flush=True)`,
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

export function buildApiExamples(baseUrl: string, model: string, mode: ApiExampleMode): ApiExamples {
  const examples = buildExamples(baseUrl, model, mode);
  // Authentication is optional on loopback but required for the public gateway.
  // Keep the same copyable examples valid when served from a remote origin.
  examples.curl = examples.curl.replace("\n", '\n  -H "Authorization: Bearer YOUR_API_KEY" \\\n');
  examples.javascript = examples.javascript.replace(/headers: \{/g, 'headers: { "Authorization": "Bearer YOUR_API_KEY",');
  if (examples.javascript.includes('fetch(') && !examples.javascript.includes('"Authorization"')) {
    examples.javascript = examples.javascript.replace('method: "POST",', 'method: "POST",\n  headers: { "Authorization": "Bearer YOUR_API_KEY" },');
  }
  return examples;
}
