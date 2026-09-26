(() => {
  const forms = document.querySelectorAll('form[data-json]');
  const fieldValue = field => {
    if (field.type === 'number') return field.value === '' ? 0 : Number(field.value);
    if (field.type === 'checkbox') return field.checked;
    return field.value;
  };
  const payload = form => {
    const output = {};
    for (const field of form.elements) {
      if (!field.name || field.disabled || field.name === 'csrf_token' || field.name === '_method' || field.type === 'submit' || field.type === 'button') continue;
      if ((field.type === 'radio' || field.type === 'checkbox') && !field.checked) continue;
      output[field.name] = fieldValue(field);
    }
    return output;
  };
  const renderResult = (box, data) => {
    box.replaceChildren();
    const heading = document.createElement('strong');
    heading.textContent = data?.dry_run ? 'Dry-run result' : 'Action complete';
    box.append(heading);
    const details = [
      data?.validation?.valid === false ? 'Publication is blocked.' : data?.validation?.valid === true ? 'Validation passed.' : '',
      data?.version !== undefined ? `Version: ${data.version}.` : '',
      data?.epub?.status ? `EPUB: ${data.epub.status}.` : data?.export_status ? `EPUB: ${data.export_status}.` : '',
      data?.actor?.name || data?.actor?.id ? `Actor: ${data.actor.name || data.actor.id}.` : ''
    ].filter(Boolean);
    const messages = [...(data?.validation?.errors || []), ...(data?.validation?.warnings || []), ...(data?.notes || [])].map(item => typeof item === 'string' ? item : item.message || item.code).filter(Boolean);
    for (const text of [...details, ...messages]) {
      const paragraph = document.createElement('p');
      paragraph.textContent = text;
      box.append(paragraph);
    }
  };
  const showError = (form, error) => {
    let box = form.querySelector('[data-form-status]');
    if (!box) {
      box = document.createElement('div');
      box.dataset.formStatus = '';
      box.setAttribute('role', 'alert');
      box.className = 'error-summary';
      box.tabIndex = -1;
      form.prepend(box);
    }
    box.textContent = error.message || 'The request could not be completed.';
    for (const [name, message] of Object.entries(error.fields || {})) {
      const field = form.elements.namedItem(name);
      if (field && 'setAttribute' in field) {
        field.setAttribute('aria-invalid', 'true');
        field.setAttribute('aria-errormessage', `error-${form.id || 'form'}-${name}`);
        let detail = document.getElementById(`error-${form.id || 'form'}-${name}`);
        if (!detail) {
          detail = document.createElement('span');
          detail.id = `error-${form.id || 'form'}-${name}`;
          detail.className = 'field-error';
          field.insertAdjacentElement('afterend', detail);
        }
        detail.textContent = message;
      }
    }
    box.focus();
  };
  for (const form of forms) {
    form.addEventListener('submit', async event => {
      event.preventDefault();
      const submitter = event.submitter;
      const method = submitter?.dataset.method || form.elements.namedItem('_method')?.value || form.method || 'POST';
      const action = submitter?.hasAttribute('formaction') ? submitter.formAction : form.action;
      let csrf = form.elements.namedItem('csrf_token')?.value || '';
      const usesSessionCSRF = !/\/api\/v1\/auth\/(?:login|register)$/.test(new URL(action).pathname);
      if (usesSessionCSRF) {
        try {
          csrf = sessionStorage.getItem('ccarp-session-csrf') || csrf;
        } catch {}
      }
      const status = form.querySelector('[data-form-status]');
      if (status) status.textContent = 'Saving...';
      if (submitter) submitter.disabled = true;
      try {
        const response = await fetch(action, {
          method: method.toUpperCase(),
          credentials: 'same-origin',
          headers: { accept: 'application/json', 'content-type': 'application/json', 'x-csrf-token': csrf },
          body: JSON.stringify(payload(form))
        });
        const envelope = response.status === 204 ? null : await response.json().catch(() => null);
        if (!response.ok) throw envelope?.error || { message: 'The request could not be completed.' };
        const data = envelope?.data;
        if (data?.csrf_token) {
          try {
            sessionStorage.setItem('ccarp-session-csrf', data.csrf_token);
          } catch {}
        }
        if (status && data && (data.dry_run !== undefined || data.validation)) {
          renderResult(status, data);
          if (submitter) submitter.disabled = false;
          return;
        }
        const redirect = form.dataset.success;
        if (redirect) {
          const target = redirect.replace(':id', data?.id || data?.exam?.id || data?.note?.id || '');
          window.location.assign(target);
        } else {
          window.location.reload();
        }
      } catch (error) {
        showError(form, error);
        if (submitter) submitter.disabled = false;
      }
    });
  }
})();
