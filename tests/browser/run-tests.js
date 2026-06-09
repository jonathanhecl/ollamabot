const http = require('http');
const { spawn, execSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const PORT_MOCK_OLLAMA = 11435;
const PORT_GO_SERVER = 8081;

let mockOllamaServer;
let goServerProcess;

// 1. Start Mock Ollama Server
function startMockOllama() {
  console.log('Starting Mock Ollama Server on port', PORT_MOCK_OLLAMA);
  
  mockOllamaServer = http.createServer((req, res) => {
    console.log(`[Mock Ollama] ${req.method} ${req.url}`);
    
    // Set headers
    res.setHeader('Content-Type', 'application/json');
    res.setHeader('Connection', 'close');
    
    const handleRequest = () => {
      if (req.method === 'GET' && req.url === '/api/version') {
        res.writeHead(200);
        res.end(JSON.stringify({ version: '0.1.48' }));
        return;
      }
      
      if (req.method === 'GET' && req.url === '/api/tags') {
        res.writeHead(200);
        res.end(JSON.stringify({
          models: [
            {
              name: 'mock-model',
              model: 'mock-model',
              details: {
                parent_model: '',
                format: 'gguf',
                family: 'llama',
                families: ['llama'],
                parameter_size: '8B',
                quantization_level: 'Q4_K_M'
              }
            }
          ]
        }));
        return;
      }
      
      if (req.method === 'GET' && req.url === '/api/ps') {
        res.writeHead(200);
        res.end(JSON.stringify({ models: [] }));
        return;
      }
      
      if (req.method === 'POST' && req.url === '/api/show') {
        res.writeHead(200);
        res.end(JSON.stringify({
          license: '',
          system: '',
          parameters: '',
          template: '',
          details: {
            parent_model: '',
            format: 'gguf',
            family: 'llama',
            families: ['llama'],
            parameter_size: '8B',
            quantization_level: 'Q4_K_M'
          },
          model_info: {
            'general.architecture': 'llama',
            'llama.context_length': 8192
          },
          projector_info: {
            'clip.has_vision_encoder': true,
            'clip.has_audio_encoder': true
          },
          capabilities: ["completion", "tools", "thinking", "vision", "audio", "embedding"]
        }));
        return;
      }
      
      if (req.method === 'POST' && req.url === '/api/chat') {
        res.writeHead(200);
        // Stream some responses
        const chunk1 = {
          model: 'mock-model',
          created_at: new Date().toISOString(),
          message: {
            role: 'assistant',
            content: 'Here is some code:\n```javascript\nconsole.log(\'hello\');\n```'
          },
          done: true
        };
        res.write(JSON.stringify(chunk1) + '\n');
        res.end();
        return;
      }
      
      res.writeHead(404);
      res.end(JSON.stringify({ error: 'Not Found' }));
    };

    if (req.method === 'POST' || req.method === 'PUT') {
      let body = '';
      req.on('data', chunk => {
        body += chunk.toString();
      });
      req.on('end', () => {
        handleRequest();
      });
    } else {
      handleRequest();
    }
  });
  
  mockOllamaServer.listen(PORT_MOCK_OLLAMA);
}

// 2. Setup Test Environment Configuration
function createEnvFile() {
  const envPath = path.join(__dirname, '.env.test');
  const envContent = `OLLAMA_BASE_URL=http://localhost:${PORT_MOCK_OLLAMA}
SERVER_PORT=${PORT_GO_SERVER}
SERVER_ENABLED=true
WEB_SEARCH_ENABLED=false
SESSION_AUTO_NAME=false
OLLAMA_MODEL_DEFAULT=mock-model
WORKSPACE=./tests/browser/workspace
SESSIONS_PATH=./tests/browser/sessions
MEMORY_PATH=./tests/browser/memory
SKILLS_PATH=./tests/browser/skills
SERVER_PASSWORD=testpass
`;
  fs.writeFileSync(envPath, envContent);
  console.log('Created temporary .env.test configuration');
}

// 3. Compile Go Server
function buildGoServer() {
  console.log('Compiling Go server...');
  const outputExe = path.join(__dirname, 'test_ollamabot.exe');
  execSync(`go build -o "${outputExe}" ./cmd/ollamabot`, { stdio: 'inherit', cwd: path.join(__dirname, '../..') });
}

// 4. Start Go Server
function startGoServer() {
  console.log('Starting Go server...');
  const exePath = path.join(__dirname, 'test_ollamabot.exe');
  const envFile = path.join(__dirname, '.env.test');
  
  goServerProcess = spawn(exePath, ['--env', envFile], {
    cwd: path.join(__dirname, '../..'),
    stdio: 'pipe'
  });
  
  goServerProcess.stdout.on('data', (data) => {
    console.log(`[Go Server] ${data.toString().trim()}`);
  });
  
  goServerProcess.stderr.on('data', (data) => {
    console.error(`[Go Server Error] ${data.toString().trim()}`);
  });
}

// 5. Poll health check
async function waitForGoServer() {
  const url = `http://localhost:${PORT_GO_SERVER}/api/health`;
  const maxAttempts = 15;
  const delay = 1000;
  
  console.log('Waiting for Go server to become healthy...');
  for (let i = 0; i < maxAttempts; i++) {
    try {
      const response = await new Promise((resolve, reject) => {
        const req = http.get(url, {
          headers: { 'X-Server-Password': 'testpass' }
        }, (res) => {
          if (res.statusCode === 200) resolve(true);
          else resolve(false);
        });
        req.on('error', () => resolve(false));
      });
      
      if (response) {
        console.log('Go server is healthy and ready!');
        return;
      }
    } catch (e) {
      // Ignore
    }
    await new Promise((r) => setTimeout(r, delay));
  }
  throw new Error('Timeout waiting for Go server healthcheck');
}

// Cleanup
function cleanup() {
  console.log('Cleaning up servers and temporary files...');
  if (goServerProcess) {
    goServerProcess.kill();
  }
  if (mockOllamaServer) {
    mockOllamaServer.close();
  }
  
  const exePath = path.join(__dirname, 'test_ollamabot.exe');
  if (fs.existsSync(exePath)) {
    try {
      fs.unlinkSync(exePath);
    } catch (e) {
      console.warn('Could not delete test_ollamabot.exe:', e.message);
    }
  }
  
  const envPath = path.join(__dirname, '.env.test');
  if (fs.existsSync(envPath)) {
    fs.unlinkSync(envPath);
  }
  
  // Clean up created folders
  const workspacePath = path.join(__dirname, 'workspace');
  if (fs.existsSync(workspacePath)) {
    fs.rmSync(workspacePath, { recursive: true, force: true });
  }
  const sessionsPath = path.join(__dirname, 'sessions');
  if (fs.existsSync(sessionsPath)) {
    fs.rmSync(sessionsPath, { recursive: true, force: true });
  }
  const memoryPath = path.join(__dirname, 'memory');
  if (fs.existsSync(memoryPath)) {
    fs.rmSync(memoryPath, { recursive: true, force: true });
  }
  const skillsPath = path.join(__dirname, 'skills');
  if (fs.existsSync(skillsPath)) {
    fs.rmSync(skillsPath, { recursive: true, force: true });
  }
  console.log('Cleanup completed');
}

// Main Runner
async function main() {
  try {
    // A. Generate asset first
    console.log('Generating assets...');
    execSync('node generate-assets.js', { cwd: __dirname, stdio: 'inherit' });

    // B. Ensure Playwright packages are installed
    if (!fs.existsSync(path.join(__dirname, 'node_modules'))) {
      console.log('node_modules not found. Installing test dependencies...');
      execSync('npm install', { cwd: __dirname, stdio: 'inherit' });
    }
    
    // C. Install playwright browsers (chromium)
    console.log('Installing Playwright Chromium browser...');
    execSync('npx playwright install chromium', { cwd: __dirname, stdio: 'inherit' });
    
    // D. Start mock services
    startMockOllama();
    createEnvFile();
    buildGoServer();
    startGoServer();
    
    // E. Wait for Go Server
    await waitForGoServer();
    
    // F. Run Playwright tests
    console.log('Running tests...');
    const playwright = spawn('npx', ['playwright', 'test'], {
      cwd: __dirname,
      stdio: 'inherit',
      shell: true
    });

    const exitCode = await new Promise((resolve) => {
      playwright.on('close', (code) => {
        resolve(code);
      });
    });

    if (exitCode === 0) {
      console.log('🎉 Integration tests PASSED successfully!');
      process.exitCode = 0;
    } else {
      throw new Error(`npx playwright test failed with exit code ${exitCode}`);
    }
  } catch (error) {
    console.error('❌ Test run FAILED:', error.message);
    process.exitCode = 1;
  } finally {
    cleanup();
  }
}

main();
