async function fetchServices() {
  const res = await fetch('/api/services');
  if (!res.ok) {
    document.getElementById('services').innerText = 'Failed to load services';
    return;
  }
  const data = await res.json();
  renderServices(data);
}

function renderServices(data) {
  const container = document.getElementById('services');
  container.innerHTML = '';
  const keys = Object.keys(data);
  if (keys.length === 0) {
    container.innerText = 'No services registered.';
    return;
  }
  keys.forEach(task => {
    const box = document.createElement('div');
    box.className = 'task-box';
    const h = document.createElement('h3');
    h.innerText = task;
    box.appendChild(h);

    const ul = document.createElement('ul');
    data[task].forEach(entry => {
      const li = document.createElement('li');
      li.innerText = `${entry.Address} (cap=${entry.Capacity}) last=${entry.LastHeartbeat}`;
      ul.appendChild(li);
    });
    box.appendChild(ul);

    const btn = document.createElement('button');
    btn.innerText = 'Query';
    btn.onclick = async () => {
      btn.disabled = true;
      const r = await fetch(`/api/services/${encodeURIComponent(task)}/query`);
      if (!r.ok) {
        alert('No available service');
        btn.disabled = false;
        return;
      }
      const j = await r.json();
      alert(`Resolved: ${j.address}`);
      btn.disabled = false;
    };
    box.appendChild(btn);

    container.appendChild(box);
  });
}

window.addEventListener('load', () => {
  fetchAll();
  setInterval(fetchAll, 5000);
});

async function fetchMetrics() {
  const res = await fetch('/api/metrics');
  if (!res.ok) return null;
  return res.json();
}

async function fetchAll() {
  const [svcRes, metrics] = await Promise.all([fetch('/api/services'), fetchMetrics()]);
  if (svcRes.ok) {
    const data = await svcRes.json();
    renderServices(data);
    renderMetrics(metrics, data);
  }
}

function renderMetrics(metrics, servicesData) {
  const container = document.getElementById('metrics');
  container.innerHTML = '';
  // Global cards
  const totalTasks = servicesData ? Object.keys(servicesData).length : 0;
  const totalQueries = metrics ? Object.values(metrics).reduce((sum, m) => sum + (m.queries||0), 0) : 0;
  const totalHits = metrics ? Object.values(metrics).reduce((sum, m) => sum + (m.cache_hits||0), 0) : 0;
  const totalMisses = metrics ? Object.values(metrics).reduce((sum, m) => sum + (m.cache_misses||0), 0) : 0;

  container.appendChild(createCard('Tasks', totalTasks));
  container.appendChild(createCard('Total Queries', totalQueries));
  container.appendChild(createCard('Cache Hits', totalHits));
  container.appendChild(createCard('Cache Misses', totalMisses));

  // Per-task small cards
  if (metrics) {
    Object.keys(metrics).forEach(task => {
      const m = metrics[task];
      const c = document.createElement('div');
      c.className = 'metric-card small';
      c.innerHTML = `<strong>${task}</strong><div class="muted">avg ${m.avg_response_ms.toFixed(1)} ms • ${m.queries} q</div>`;
      container.appendChild(c);
    });
  }
}

function createCard(title, value) {
  const c = document.createElement('div');
  c.className = 'metric-card';
  c.innerHTML = `<div class="metric-title">${title}</div><div class="metric-value">${value}</div>`;
  return c;
}
