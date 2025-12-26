/* Copyright (c) 2025 Berik Ashimov */

(() => {
  const attachConfirm = () => {
    const modal = document.getElementById('confirm-modal');
    if (!modal) {
      return;
    }
    const messageNode = modal.querySelector('[data-confirm-message]');
    const confirmButton = modal.querySelector('[data-modal-confirm]');
    const closeButtons = modal.querySelectorAll('[data-modal-close]');
    let pendingForm = null;
    let pendingSubmitter = null;

    const openModal = (message, form, submitter) => {
      pendingForm = form;
      pendingSubmitter = submitter || null;
      if (messageNode) {
        messageNode.textContent = message || 'Подтвердите действие.';
      }
      modal.classList.add('is-open');
      modal.setAttribute('aria-hidden', 'false');
      confirmButton?.focus();
    };

    const closeModal = () => {
      modal.classList.remove('is-open');
      modal.setAttribute('aria-hidden', 'true');
      if (pendingForm) {
        pendingForm.removeAttribute('data-confirm-bypass');
      }
      pendingForm = null;
      pendingSubmitter = null;
    };

    confirmButton?.addEventListener('click', () => {
      if (pendingForm) {
        pendingForm.setAttribute('data-confirm-bypass', '1');
        if (typeof pendingForm.requestSubmit === 'function') {
          if (pendingSubmitter instanceof HTMLElement) {
            pendingForm.requestSubmit(pendingSubmitter);
          } else {
            pendingForm.requestSubmit();
          }
        } else {
          pendingForm.submit();
        }
      }
      closeModal();
    });

    closeButtons.forEach((button) => {
      button.addEventListener('click', closeModal);
    });

    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && modal.classList.contains('is-open')) {
        closeModal();
      }
    });

    document.addEventListener(
      'submit',
      (event) => {
        const form = event.target;
        if (!(form instanceof HTMLFormElement)) {
          return;
        }
        const message = form.getAttribute('data-confirm');
        if (!message) {
          return;
        }
        if (form.hasAttribute('data-confirm-bypass')) {
          return;
        }
        event.preventDefault();
        event.stopPropagation();
        const submitter = event.submitter instanceof HTMLElement ? event.submitter : null;
        openModal(message, form, submitter);
      },
      true
    );

    window.addEventListener('pageshow', () => {
      if (modal.classList.contains('is-open')) {
        closeModal();
      }
    });
  };

  const applyReveal = () => {
    document.body.classList.add('is-ready');
    const blocks = Array.from(
      document.querySelectorAll('.page-head, .card, .table-responsive, .list-group, pre')
    );
    blocks.forEach((block, index) => {
      block.classList.add('reveal');
      block.style.animationDelay = `${Math.min(index * 70, 420)}ms`;
    });
  };

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => {
      attachConfirm();
      applyReveal();
    }, { once: true });
  } else {
    attachConfirm();
    applyReveal();
  }
})();
