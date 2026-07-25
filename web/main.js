/* sandbox-cli landing page — interactions. Vanilla JS, no dependencies. */
(function () {
  'use strict';

  var root = document.documentElement;
  var reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
  var $  = function (s, c) { return (c || document).querySelector(s); };
  var $$ = function (s, c) { return Array.prototype.slice.call((c || document).querySelectorAll(s)); };

  /* ---------------------------------------------------------------- theme */
  var toggle = $('#themeToggle');
  var sun = toggle && $('.icon-sun', toggle);
  var moon = toggle && $('.icon-moon', toggle);

  function syncThemeIcon() {
    var dark = root.classList.contains('dark');
    if (sun) sun.style.display = dark ? 'none' : 'block';
    if (moon) moon.style.display = dark ? 'block' : 'none';
  }
  syncThemeIcon();

  if (toggle) {
    toggle.addEventListener('click', function () {
      root.classList.toggle('dark');
      try { localStorage.setItem('theme', root.classList.contains('dark') ? 'dark' : 'light'); } catch (e) {}
      syncThemeIcon();
    });
  }

  try {
    matchMedia('(prefers-color-scheme: dark)').addEventListener('change', function (e) {
      if (localStorage.getItem('theme')) return;   // explicit choice wins
      root.classList.toggle('dark', e.matches);
      syncThemeIcon();
    });
  } catch (e) {}

  /* --------------------------------------------------------------- header */
  var header = $('#siteHeader');
  function onScroll() { if (header) header.classList.toggle('scrolled', window.scrollY > 8); }
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });

  var menuToggle = $('#menuToggle');
  var mobileMenu = $('#mobileMenu');
  if (menuToggle && mobileMenu) {
    menuToggle.addEventListener('click', function () {
      var open = mobileMenu.classList.toggle('open');
      menuToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
    mobileMenu.addEventListener('click', function (e) {
      if (e.target.tagName === 'A') {
        mobileMenu.classList.remove('open');
        menuToggle.setAttribute('aria-expanded', 'false');
      }
    });
  }

  /* ==================================================== CONTAINMENT DEMO ==
     Fire a command at the boundary and watch where it lands.
     ===================================================================== */
  var wall = $('#wall'), packet = $('#packet'), log = $('#log');
  var counter = $('#verdictCount'), stage = $('#stage');
  var shots = $$('.shot');

  var blockedCount = 0, busy = false, autoTimer = null, userEngaged = false;

  var VERDICTS = {
    ssh:  { tag: 'BLOCKED', msg: 'no such file — ~/.ssh was never mounted' },
    aws:  { tag: 'BLOCKED', msg: 'no such file — ~/.aws was never mounted' },
    home: { tag: 'BLOCKED', msg: 'deleted /sandbox/home — an empty, throwaway dir' },
    net:  { tag: 'BLOCKED', msg: 'egress denied — host not in --allow list' },
    ws:   { tag: 'ALLOWED', msg: 'ran in /workspace — exactly what you asked for' }
  };

  /* Classify an arbitrary typed command against the boundary. */
  var RULES = [
    { re: /(~|\$HOME|\/home\/\w+|\/Users\/\w+)\/\.ssh|ssh-add|id_rsa|id_ed25519|known_hosts/i,
      target: 'ssh', msg: 'no such file — ~/.ssh was never mounted' },
    { re: /\.aws|aws\s+(configure|sts|s3)|credentials|\.config\/gcloud|gcloud\s+auth/i,
      target: 'aws', msg: 'no such file — cloud credentials were never mounted' },
    { re: /\.(env|netrc|npmrc|pypirc|docker\/config)|keychain|Cookies|Login Data|\.gnupg|\.kube/i,
      target: 'aws', msg: 'not present — only /workspace is mounted from the host' },
    { re: /rm\s+(-[a-z]*\s+)*(~|\/|\$HOME|\/home|\/Users)(\s|\/|$)/i,
      target: 'home', msg: 'hit /sandbox/home — ephemeral, and gone at exit anyway' },
    { re: /(curl|wget|nc|ncat|telnet)\b|\|\s*(sh|bash)\b|pip\s+install|npm\s+i(nstall)?\s+-g/i,
      target: 'net', msg: 'egress denied — host not in the --allow list' },
    { re: /\/etc\/(passwd|shadow|hosts)|\/proc\/|\/sys\/|dmesg|mount\b/i,
      target: 'home', msg: 'container view only — that is not your host' },
    { re: /docker|kubectl|systemctl|launchctl/i,
      target: 'net', msg: 'no socket, no daemon — the container cannot reach them' },
    { re: /sudo|su\s+-|chmod\s+\+s|usermod/i,
      target: 'home', msg: 'running as the unprivileged sandbox user' }
  ];

  var SAFE_RE = /^(npm|yarn|pnpm|bun|go|cargo|make|pytest|python3?|node|deno|ruby|rake|mvn|gradle|dotnet|git|ls|cat|grep|rg|find|sed|awk|vim|touch|mkdir|echo|pwd|tree|wc|head|tail|diff|jq)\b/i;

  function classify(cmd) {
    var c = cmd.trim();
    if (!c) return null;
    for (var i = 0; i < RULES.length; i++) {
      if (RULES[i].re.test(c)) {
        return { safe: false, target: RULES[i].target, tag: 'BLOCKED', msg: RULES[i].msg };
      }
    }
    if (SAFE_RE.test(c)) {
      return { safe: true, target: 'ws', tag: 'ALLOWED', msg: 'ran in /workspace — the project it was editing anyway' };
    }
    return { safe: true, target: 'ws', tag: 'ALLOWED', msg: 'ran inside the container — blast radius is /workspace' };
  }

  function addLog(kind, cmd, msg) {
    if (!log) return;
    var idle = $('.log-idle', log);
    if (idle) idle.remove();
    var line = document.createElement('div');
    line.className = 'log-line ' + (kind === 'ALLOWED' ? 'allowed' : 'blocked');
    var tag = document.createElement('span');
    tag.className = 'log-tag';
    tag.textContent = kind;
    var body = document.createElement('span');
    body.className = 'log-msg';
    body.textContent = '$ ' + cmd + '  →  ' + msg;
    line.appendChild(tag);
    line.appendChild(body);
    log.appendChild(line);
    while (log.children.length > 4) log.removeChild(log.firstChild);
  }

  function clearHighlights() {
    if (stage) $$('.path', stage).forEach(function (p) { p.classList.remove('hit', 'reached'); });
  }

  function run(cmd, safe, target, tag, msg) {
    if (busy || !wall || !packet) return;
    busy = true;

    clearHighlights();
    wall.classList.remove('impact', 'pass');
    packet.textContent = cmd.length > 34 ? cmd.slice(0, 33) + '…' : cmd;
    packet.classList.toggle('safe', safe);

    var animate = !reduceMotion && window.innerWidth > 860;
    if (!animate) { settle(safe, target, cmd, tag, msg); return; }

    packet.style.transition = 'none';
    packet.style.left = '4%';
    packet.style.opacity = '0';
    void packet.offsetWidth;                       // reflow before animating

    packet.style.transition = 'left .62s cubic-bezier(.4,.05,.35,1), opacity .18s ease';
    packet.style.opacity = '1';
    packet.style.left = safe ? '78%' : '43%';

    window.setTimeout(function () {
      if (safe) {
        packet.style.transition = 'opacity .3s ease';
        packet.style.opacity = '0';
      } else {
        packet.style.transition = 'left .42s cubic-bezier(.2,.9,.3,1), opacity .42s ease';
        packet.style.left = '12%';
        packet.style.opacity = '0';
      }
      settle(safe, target, cmd, tag, msg);
    }, 640);
  }

  function settle(safe, target, cmd, tag, msg) {
    if (wall) wall.classList.add(safe ? 'pass' : 'impact');
    var node = stage && $('.path[data-path="' + target + '"]', stage);
    if (node) node.classList.add(safe ? 'reached' : 'hit');

    addLog(tag, cmd, msg);

    if (!safe) {
      blockedCount++;
      if (counter) counter.textContent = blockedCount + (blockedCount === 1 ? ' blocked' : ' blocked');
    }
    window.setTimeout(function () {
      if (wall) wall.classList.remove('impact', 'pass');
      busy = false;
    }, 700);
  }

  function fireButton(btn) {
    var v = VERDICTS[btn.getAttribute('data-target')] || VERDICTS.ssh;
    run(btn.getAttribute('data-cmd'), btn.getAttribute('data-kind') === 'safe',
        btn.getAttribute('data-target'), v.tag, v.msg);
  }

  shots.forEach(function (btn) {
    btn.addEventListener('click', function () {
      userEngaged = true;
      if (autoTimer) { clearInterval(autoTimer); autoTimer = null; }
      fireButton(btn);
    });
  });

  /* --- free-text command input --- */
  var promptInput = $('#promptInput'), promptGo = $('#promptGo'), promptRow = $('#promptRow');

  function submitPrompt() {
    if (!promptInput) return;
    var cmd = promptInput.value.trim();
    if (!cmd) return;
    userEngaged = true;
    if (autoTimer) { clearInterval(autoTimer); autoTimer = null; }
    var v = classify(cmd);
    if (v) run(cmd, v.safe, v.target, v.tag, v.msg);
    promptInput.value = '';
  }

  if (promptInput) {
    promptInput.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); submitPrompt(); }
    });
    promptInput.addEventListener('focus', function () { if (promptRow) promptRow.classList.add('focused'); });
    promptInput.addEventListener('blur',  function () { if (promptRow) promptRow.classList.remove('focused'); });
  }
  if (promptGo) promptGo.addEventListener('click', submitPrompt);

  /* --- gentle autoplay until the visitor takes over --- */
  if (shots.length && 'IntersectionObserver' in window && !reduceMotion) {
    var demoSeen = false;
    var demoIO = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting || demoSeen) return;
        demoSeen = true;
        demoIO.disconnect();
        var i = 0;
        window.setTimeout(function () {
          if (userEngaged) return;
          fireButton(shots[0]);
          autoTimer = window.setInterval(function () {
            if (userEngaged) { clearInterval(autoTimer); autoTimer = null; return; }
            i = (i + 1) % shots.length;
            fireButton(shots[i]);
            if (i === shots.length - 1) { clearInterval(autoTimer); autoTimer = null; }
          }, 2200);
        }, 700);
      });
    }, { threshold: 0.35 });
    var consoleEl = $('#boundary');
    if (consoleEl) demoIO.observe(consoleEl);
  }

  /* ====================================================== BLAST RADIUS ====
     Flip containment on/off and watch the host tree change state.
     ===================================================================== */
  var radiusSwitch = $('#radiusSwitch'), fsTree = $('#fsTree');
  var rtOn = $('#rtOn'), rtOff = $('#rtOff'), radiusCaption = $('#radiusCaption');
  var contained = false;

  function paintRadius() {
    if (!fsTree) return;
    $$('.fs-row', fsTree).forEach(function (row) {
      var isWs = row.getAttribute('data-fs') === 'ws';
      var state = $('.fs-state', row);
      row.classList.remove('reachable', 'blocked', 'workspace');
      if (!contained) {
        row.classList.add('reachable');
        if (state) state.textContent = 'reachable';
      } else if (isWs) {
        row.classList.add('workspace');
        if (state) state.textContent = 'mounted';
      } else {
        row.classList.add('blocked');
        if (state) state.textContent = 'not mounted';
      }
    });
    if (radiusSwitch) {
      radiusSwitch.classList.toggle('on', contained);
      radiusSwitch.setAttribute('aria-checked', contained ? 'true' : 'false');
    }
    if (rtOn)  { rtOn.classList.toggle('active', contained);  rtOn.classList.toggle('on-active', contained); }
    if (rtOff) { rtOff.classList.toggle('active', !contained); rtOff.classList.toggle('off-active', !contained); }
    if (radiusCaption) {
      radiusCaption.textContent = contained
        ? 'One path mounted. The rest was never in the container to begin with.'
        : 'Everything above is reachable by an agent running with “Allow All”.';
    }
  }

  if (radiusSwitch) {
    radiusSwitch.addEventListener('click', function () { contained = !contained; paintRadius(); });
    radiusSwitch.addEventListener('keydown', function (e) {
      if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); contained = !contained; paintRadius(); }
    });
  }
  paintRadius();

  /* ======================================================= DRY-RUN BUILDER
     Compose real sandbox flags into the docker argv they produce.
     ===================================================================== */
  var argvEl = $('#argv'), builderCmd = $('#builderCmd'), builderVerdict = $('#builderVerdict');
  var wtInput = $('#worktreeName'), allowInput = $('#allowDomain');

  var opts = { agent: 'run', worktree: false, allow: false, nopersist: false, root: false };

  var AGENT_RUN = {
    run:    { cli: 'run --dry-run -- npm test', argv: ['npm', 'test'], home: null, env: [] },
    claude: { cli: 'claude --dry-run', argv: ['claude'], home: 'claude', env: ['ANTHROPIC_API_KEY'] },
    codex:  { cli: 'codex --dry-run', argv: ['codex'], home: 'codex', env: ['OPENAI_API_KEY'] },
    aider:  { cli: 'aider --dry-run', argv: ['aider'], home: 'aider', env: ['OPENAI_API_KEY', 'ANTHROPIC_API_KEY'] }
  };

  function esc(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;'); }
  function line(html) { return '<span class="argv-line">' + html + '</span>'; }

  function renderArgv() {
    if (!argvEl) return;
    var a = AGENT_RUN[opts.agent];
    var src = opts.worktree
      ? '~/.config/sandbox/worktrees/myapp/' + ((wtInput && wtInput.value.trim()) || 'feature-a')
      : '~/projects/myapp';

    var out = [];
    out.push(line('<span class="a-cmd">docker</span> run <span class="a-flag">--rm</span> <span class="a-flag">-it</span> \\'));
    out.push(line('  <span class="a-flag">-v</span> <span class="a-path">' + esc(src) + '</span>:<span class="a-path">/workspace</span> \\'));
    out.push(line('  <span class="a-flag">-w</span> /workspace \\'));
    out.push(line('  <span class="a-flag">-e</span> HOME=/sandbox/home \\'));

    if (a.home && !opts.nopersist) {
      out.push(line('  <span class="a-flag">-v</span> <span class="a-path">~/.config/sandbox/agents/' + esc(a.home) + '</span>:<span class="a-path">/sandbox/home</span> \\'));
    }
    a.env.forEach(function (e) {
      out.push(line('  <span class="a-flag">-e</span> ' + esc(e) + '=<span class="a-img">$' + esc(e) + '</span> \\'));
    });

    if (opts.allow) {
      var d = (allowInput && allowInput.value.trim()) || 'api.anthropic.com';
      out.push(line('  <span class="a-flag">--network</span> sandbox-egress \\'));
      out.push(line('  <span class="a-flag">-e</span> SANDBOX_ALLOW=<span class="a-path">' + esc(d) + '</span> \\'));
    }

    if (opts.root) {
      out.push(line('  <span class="a-danger">« --user omitted: running as root »</span> \\'));
    } else {
      out.push(line('  <span class="a-flag">--user</span> sandbox \\'));
    }

    out.push(line('  <span class="a-img">sandbox-base:0.0.1</span> \\'));
    out.push(line('  <span class="a-cmd">' + esc(a.argv.join(' ')) + '</span>'));

    argvEl.innerHTML = out.join('\n');

    if (builderCmd) {
      var cli = '$ sandbox-cli ' + a.cli;
      var flags = [];
      if (opts.worktree) flags.push('--worktree ' + ((wtInput && wtInput.value.trim()) || 'feature-a'));
      if (opts.allow) flags.push('--allow ' + ((allowInput && allowInput.value.trim()) || 'api.anthropic.com'));
      if (opts.nopersist) flags.push('--no-persist-auth');
      if (opts.root) flags.push('--root');
      if (flags.length) cli = cli.replace(' --dry-run', ' ' + flags.join(' ') + ' --dry-run');
      builderCmd.textContent = cli;
    }

    if (builderVerdict) {
      var msg, warn = false;
      if (opts.root) { msg = 'Root inside the box — still only /workspace is mounted, but drop this if you can.'; warn = true; }
      else if (opts.allow) msg = 'One host path mounted, egress pinned to one domain. Tightest setting.';
      else if (opts.worktree) msg = 'Mounts an isolated worktree — the main checkout is untouched.';
      else if (opts.nopersist) msg = 'No saved login mounted. The agent starts logged out every run.';
      else msg = 'One host path mounted. Home directory unreachable.';
      builderVerdict.className = 'builder-verdict ' + (warn ? 'warn' : 'safe');
      var span = $('span', builderVerdict);
      if (span) span.textContent = msg;
    }
  }

  $$('#segAgent button').forEach(function (b) {
    b.addEventListener('click', function () {
      $$('#segAgent button').forEach(function (x) { x.classList.remove('on'); });
      b.classList.add('on');
      opts.agent = b.getAttribute('data-agent');
      renderArgv();
    });
  });

  $$('.switch-row').forEach(function (rowEl) {
    var key = rowEl.getAttribute('data-opt');
    var box = $('input', rowEl);
    if (!key || !box) return;
    box.addEventListener('change', function () {
      opts[key] = box.checked;
      rowEl.classList.toggle('on', box.checked);
      if (key === 'worktree' && wtInput) wtInput.disabled = !box.checked;
      if (key === 'allow' && allowInput) allowInput.disabled = !box.checked;
      renderArgv();
    });
  });

  [wtInput, allowInput].forEach(function (input) {
    if (input) input.addEventListener('input', renderArgv);
  });

  renderArgv();

  /* ========================================================= AGENT PICKER */
  var AGENTS = {
    claude:    { name: 'Claude Code', install: 'native installer into the persisted HOME; npm copy baked as an offline fallback', env: ['ANTHROPIC_API_KEY', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_BASE_URL', 'CLAUDE_CODE_USE_BEDROCK', 'CLAUDE_CODE_USE_VERTEX'], extra: 'The only agent with an on-screen CPU/memory gauge, injected via read-only managed settings. Also mounts your Claude history for this one project.' },
    codex:     { name: 'Codex CLI', install: '@openai/codex — baked into the base image', env: ['OPENAI_API_KEY', 'OPENAI_BASE_URL', 'CODEX_HOME'], extra: 'Use sandbox-cli stats in a second terminal for resource monitoring.' },
    gemini:    { name: 'Gemini CLI', install: '@google/gemini-cli — baked, with a HOME fallback', env: ['GEMINI_API_KEY', 'GOOGLE_API_KEY', 'GOOGLE_GENAI_USE_VERTEXAI', 'GOOGLE_CLOUD_PROJECT', 'GOOGLE_CLOUD_LOCATION'], extra: 'Reads a system settings file that outranks user and project settings — the right place for a sandbox-imposed default.' },
    opencode:  { name: 'OpenCode', install: 'opencode-ai — baked, with a HOME fallback', env: ['ANTHROPIC_API_KEY', 'OPENAI_API_KEY', 'GEMINI_API_KEY', 'GROQ_API_KEY', 'OPENROUTER_API_KEY', 'OPENCODE_CONFIG'], extra: 'No status-line hook upstream, so no on-screen gauge.' },
    cline:     { name: 'Cline', install: 'cline (npm) — installed into the persisted HOME on first run', env: ['ANTHROPIC_API_KEY', 'CLINE_API_KEY', 'OPENAI_API_KEY', 'OPENROUTER_API_KEY', 'AI_GATEWAY_API_KEY', 'V0_API_KEY'], extra: 'Lazily installed: costs the base image nothing until you actually run it.' },
    goose:     { name: 'Goose', install: 'official installer on first run (needs bzip2)', env: ['ANTHROPIC_API_KEY', 'OPENAI_API_KEY', 'GOOGLE_API_KEY', 'GROQ_API_KEY', 'OPENROUTER_API_KEY', 'GOOSE_PROVIDER', 'GOOSE_MODEL'], extra: 'Sets GOOSE_DISABLE_KEYRING=1 — a container has no Secret Service, so secrets go to the persisted home instead.' },
    crush:     { name: 'Crush', install: '@charmland/crush (npm) — on first run', env: ['ANTHROPIC_API_KEY', 'OPENAI_API_KEY', 'GEMINI_API_KEY', 'OPENROUTER_API_KEY', 'GROQ_API_KEY', 'HYPER_API_KEY'], extra: 'Also forwards AWS and Azure keys when they are set on the host.' },
    aider:     { name: 'Aider', install: 'aider-chat (PyPI) via uv — on first run', env: ['OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'GEMINI_API_KEY', 'DEEPSEEK_API_KEY', 'OPENROUTER_API_KEY'], extra: 'The first non-npm adapter. Note it writes into the workspace: a chat history file, a tags cache, and a line in .gitignore.' },
    copilot:   { name: 'GitHub Copilot CLI', install: '@github/copilot (npm) — on first run', env: ['COPILOT_GITHUB_TOKEN', 'GH_TOKEN', 'GITHUB_TOKEN', 'GH_HOST', 'COPILOT_MODEL'], extra: 'Forwards your GitHub token only when it is already set in the environment.' },
    cursor:    { name: 'Cursor CLI', install: 'vendor installer — on first run', env: ['CURSOR_API_KEY', 'CURSOR_API_ENDPOINT'], extra: 'Sets NO_OPEN_BROWSER=1 so login works in a headless container.' },
    qwen:      { name: 'Qwen Code', install: '@qwen-code/qwen-code (npm) — on first run', env: ['OPENAI_API_KEY', 'DASHSCOPE_API_KEY', 'GEMINI_API_KEY', 'OPENROUTER_API_KEY', 'BAILIAN_CODING_PLAN_API_KEY'], extra: 'Sets SANDBOX=1 and NO_BROWSER=1 for headless operation.' },
    amp:       { name: 'Amp', install: '@ampcode/cli (npm) — on first run', env: ['AMP_API_KEY', 'AMP_URL', 'AMP_LOG_LEVEL', 'AMP_SKIP_UPDATE_CHECK'], extra: 'Straightforward adapter — nothing unusual in its auth flow.' },
    continue:  { name: 'Continue CLI', install: '@continuedev/cli (npm) — runs as cn', env: ['ANTHROPIC_API_KEY', 'CONTINUE_API_BASE', 'GOOGLE_CLOUD_PROJECT'], extra: 'The binary is cn, not continue — the wrapper handles that for you.' },
    openhands: { name: 'OpenHands CLI', install: 'standalone binary from GitHub releases — on first run', env: ['LLM_API_KEY', 'LLM_MODEL', 'LLM_BASE_URL', 'ANTHROPIC_API_KEY', 'OPENAI_API_KEY'], extra: 'The LLM_* variables need --override-with-envs to take effect.' },
    droid:     { name: 'Droid', install: 'droid (npm) — on first run', env: ['FACTORY_API_KEY', 'FACTORY_API_BASE_URL', 'FACTORY_APP_BASE_URL', 'FACTORY_ENV'], extra: 'Sets FACTORY_DISABLE_KEYRING=1 for the same reason Goose does.' }
  };

  var agentGrid = $('#agentGrid'), agentDetail = $('#agentDetail');

  function renderAgent(key) {
    if (!agentDetail) return;
    var a = AGENTS[key];
    if (!a) return;
    agentDetail.innerHTML =
      '<div class="agent-detail-head">' +
        '<h3>' + esc(a.name) + '</h3>' +
        '<span class="badge badge-contained">~/.config/sandbox/agents/' + esc(key) + '</span>' +
      '</div>' +
      '<div class="agent-run"><span class="prompt">$</span><span>sandbox-cli ' + esc(key) + '</span></div>' +
      '<div class="agent-detail-grid">' +
        '<div class="adx"><h6>Install route</h6><p>' + esc(a.install) + '</p></div>' +
        '<div class="adx"><h6>Forwarded when set</h6><div class="env-chips">' +
          a.env.map(function (e) { return '<span class="env-chip set">' + esc(e) + '</span>'; }).join('') +
        '</div></div>' +
        '<div class="adx"><h6>Worth knowing</h6><p>' + esc(a.extra) + '</p></div>' +
      '</div>';
  }

  if (agentGrid) {
    $$('.agent', agentGrid).forEach(function (btn) {
      btn.addEventListener('click', function () {
        $$('.agent', agentGrid).forEach(function (x) { x.classList.remove('on'); });
        btn.classList.add('on');
        renderAgent(btn.getAttribute('data-agent'));
      });
    });
    renderAgent('claude');
  }

  /* ---------------------------------------------------------------- tabs */
  $$('.tabs').forEach(function (wrap) {
    var tabs = $$('.tab', wrap), panels = $$('.tabpanel', wrap);
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () {
        var name = tab.getAttribute('data-tab');
        tabs.forEach(function (t) {
          var on = t === tab;
          t.classList.toggle('active', on);
          t.setAttribute('aria-selected', on ? 'true' : 'false');
        });
        panels.forEach(function (p) { p.classList.toggle('active', p.getAttribute('data-panel') === name); });
      });
    });
  });

  /* ---------------------------------------------------------------- copy */
  function flashCopied(btn) {
    btn.classList.add('copied');
    var label = $('.copy-label', btn);
    var prev = label ? label.textContent : null;
    if (label) label.textContent = 'Copied';
    setTimeout(function () {
      btn.classList.remove('copied');
      if (label && prev !== null) label.textContent = prev;
    }, 1600);
  }

  function fallbackCopy(text, btn) {
    var ta = document.createElement('textarea');
    ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
    document.body.appendChild(ta); ta.select();
    try { document.execCommand('copy'); flashCopied(btn); } catch (e) {}
    document.body.removeChild(ta);
  }

  $$('.copy-btn').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var text = '';
      var sel = btn.getAttribute('data-copy-target');
      if (sel) {
        var el = $(sel);
        text = el ? el.textContent.trim() : '';
      } else if (btn.hasAttribute('data-copy-block')) {
        var pre = $('pre', btn.parentElement);
        text = pre ? pre.textContent.replace(/\n+$/, '') : '';
      }
      if (!text) return;
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () { flashCopied(btn); })
          .catch(function () { fallbackCopy(text, btn); });
      } else { fallbackCopy(text, btn); }
    });
  });

  /* ------------------------------------------------- tile spotlight ----- */
  if (!reduceMotion && matchMedia('(hover: hover)').matches) {
    $$('.tile').forEach(function (tile) {
      tile.addEventListener('pointermove', function (e) {
        var r = tile.getBoundingClientRect();
        tile.style.setProperty('--mx', (e.clientX - r.left) + 'px');
        tile.style.setProperty('--my', (e.clientY - r.top) + 'px');
      });
    });
  }

  /* ------------------------------------------------- count-up figures --- */
  function countUp(el, to, suffix) {
    if (reduceMotion) { el.textContent = to + (suffix || ''); return; }
    var start = null, dur = 900;
    function step(ts) {
      if (start === null) start = ts;
      var p = Math.min((ts - start) / dur, 1);
      var eased = 1 - Math.pow(1 - p, 3);
      el.textContent = Math.round(to * eased) + (suffix || '');
      if (p < 1) requestAnimationFrame(step);
    }
    requestAnimationFrame(step);
  }

  var trust = $('.trust');
  if (trust && 'IntersectionObserver' in window) {
    var trustIO = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        trustIO.disconnect();
        $$('.trust-item b', trust).forEach(function (b) {
          var raw = b.textContent.trim();
          var n = parseInt(raw, 10);
          if (!isNaN(n) && String(n) === raw) countUp(b, n);
        });
      });
    }, { threshold: 0.5 });
    trustIO.observe(trust);
  }

  /* -------------------------------------------------------------- reveal */
  var reveals = $$('.reveal');
  if ('IntersectionObserver' in window && !reduceMotion) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (entry) {
        if (!entry.isIntersecting) return;
        entry.target.classList.add('in');
        io.unobserve(entry.target);
      });
    }, { threshold: 0.1, rootMargin: '0px 0px -40px 0px' });
    reveals.forEach(function (el) { io.observe(el); });
  } else {
    reveals.forEach(function (el) { el.classList.add('in'); });
  }

  /* ---------------------------------------------------------------- year */
  var yr = $('#year');
  if (yr) yr.textContent = String(new Date().getFullYear());
})();
