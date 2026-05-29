#!/usr/bin/env node
// Run Claude Code with the same model environment used by cc_switch.

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';

const args = process.argv.slice(2);
const configPath = process.env.CC_SWITCH_CONFIG || path.join(os.homedir(), '.models.json');
const preferredDefaultModel = 'deepseek';
const requestedModel =
  process.env.CC_SWITCH_MODEL ||
  process.env.CLAUDE_CODE_MODEL_PROFILE ||
  process.env.CLAUDE_MODEL_PROFILE;

function readConfig() {
  if (!fs.existsSync(configPath)) {
    return null;
  }

  try {
    return JSON.parse(fs.readFileSync(configPath, 'utf8'));
  } catch (error) {
    console.error(`Error: failed to read cc_switch config ${configPath}: ${error.message}`);
    process.exit(1);
  }
}

function modelEnv(config) {
  if (!config) {
    return {};
  }

  const modelName =
    requestedModel ||
    (config.models?.[preferredDefaultModel] ? preferredDefaultModel : config.defaultModel);
  if (!modelName) {
    return {};
  }

  const model = config.models?.[modelName];
  if (!model) {
    console.error(`Error: cc_switch model "${modelName}" not found in ${configPath}`);
    process.exit(1);
  }

  const token = model.env?.ANTHROPIC_AUTH_TOKEN;
  if (token && (token.includes('YOUR_') || token === 'placeholder')) {
    console.error(`Error: cc_switch model "${modelName}" has a placeholder API token`);
    process.exit(1);
  }

  return model.env || {};
}

const env = {
  ...process.env,
  ...modelEnv(readConfig()),
};

const child = spawn('claude', args, {
  cwd: process.cwd(),
  env,
  stdio: 'inherit',
});

child.on('error', (error) => {
  if (error.code === 'ENOENT') {
    console.error('Error: claude command not found');
  } else {
    console.error(`Error: failed to start claude: ${error.message}`);
  }
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 1);
});
