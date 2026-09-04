# Sam's message, verbatim (2026-09-05)

Source: chat message pasting an AWX forum post (AWX modernization series,
"Refactoring AWX into a Pluggable, Service-Oriented Architecture") plus a
terminal transcript and a follow-up message including Ansible's
`ansible/playbook/task.py`. Preserved verbatim below for later reference.
Image attachment (`AAP Pluggable Architecture` diagram) is described, not
reproduced, since this file is text-only.

---

[01:09:53] sam@helix /work/clones
> git clone https://github.com/ansible/ansible.git
Cloning into 'ansible'...
remote: Enumerating objects: 644471, done.
remote: Counting objects: 100% (21/21), done.
remote: Compressing objects: 100% (21/21), done.
remote: Total 644471 (delta 5), reused 2 (delta 0), pack-reused 644450 (from 3)
Receiving objects: 100% (644471/644471), 251.86 MiB | 9.60 MiB/s, done.
Resolving deltas: 100% (427812/427812), done.
[01:12:50] sam@helix /work/clones
> git clone https://github.com/ansible/awx.git
Cloning into 'awx'...
remote: Enumerating objects: 331428, done.
remote: Counting objects: 100% (399/399), done.
remote: Compressing objects: 100% (256/256), done.
remote: Total 331428 (delta 259), reused 153 (delta 143), pack-reused 331029 (from 3)
Receiving objects: 100% (331428/331428), 346.81 MiB | 12.94 MiB/s, done.
Resolving deltas: 100% (254095/254095), done.
[01:14:59] sam@helix /work/clones
---
[Image #2]

Hi folks,

In our previous post, we discussed the upcoming changes we're making to the AWX project. Today, we'd like to dive deeper into one of the most significant changes: refactoring AWX into a pluggable, service-oriented architecture.
Current State of AWX

AWX is currently structured as a monolithic Django application. It consists of several entry points, each running as individual processes, including our API, Job Dispatcher, Event Processor, and Websocket subsystem. Alongside these, AWX relies on various ancillary processes such as Redis, Rsyslog, Receptor, Nginx, Uwsgi, and Daphne.
Challenges with the Current Architecture

Over time, it has become increasingly challenging to implement changes due to the large, self-contained nature of the application. This monolithic structure has not only slowed down our development process but also made it difficult to ensure efficient code reuse across our other applications. Consequently, this impacts both our development efficiency and the ability to quickly iterate on features and fixes. As we continue to extend capabilities across Ansible Automation Platform we cannot afford these challenges and maintain upstream projects the way they are currently structured.
Proposed Changes

To address these challenges, we are beginning to transition AWX towards a more pluggable, service-oriented architecture. By breaking down the monolithic structure into smaller, modular services, we aim to enhance both flexibility and maintainability. This shift will enable us to make changes more swiftly and efficiently in AWX while also improving our ability to reuse code across various projects leveraged by Ansible Automation Platform.
Implementation Plan

Our implementation plan involves several key steps:

    Identifying Modular Services: We will start by identifying the core functionalities that can be extracted into individual plugins and/or services.
    Creating a Communication Framework: Establish a robust communication framework to ensure seamless interaction between these services.
    Gradual Transition: Implement the new architecture in phases to allow for continuous feedback and improvement.
    Community Collaboration: Engage with our community throughout the process to gather feedback and ensure the new architecture meets the needs of our users.

Future-Looking Architecture

1316×628 38.8 KB

Expected Outcomes

By refactoring AWX into a service-oriented architecture, we expect several positive outcomes:

    Improved Flexibility: Easier to implement changes and add new features.
    Enhanced Maintainability: Simplified codebase with clear service boundaries.
    Better Code Reuse: More efficient reuse of code across different projects.
    Active Community Participation: Greater opportunity for community members to contribute and shape the project.

Call to Action

We invite feedback and collaboration with us on this significant transformation by replying to this forum post.

Join the discussions on our forum, share your thoughts, and help shape the future of awx .
Conclusion

These architectural changes are a crucial step towards a more modern and scalable AWX. We look forward to your participation and support as we embark on this journey together.

Stay updated by joining The Forum, following the
News & Announcements and
awx tags.

Thank you for your continued support.
Links in this AWX update series

    Blog: Upcoming Changes to the AWX Project
    Streamlining AWX Releases
    Refactoring AWX into a Pluggable, Service-Oriented Architecture (this post)
    Upcoming changes to AWX Operator installation methods
    AWX UI and credential types transitioning to the new pluggable architecture
    AWX modernization: Moving forward
    AWX modernization: Ansible UI
    AWX modernization: Ansible Jewel

Useful links

    2024-05-30 Blog: Upcoming Changes to the AWX Project
    2024-07-01 Streamlining AWX Releases
    The Forum: AWX topics
    The Forum: Newsletter Category
---
Taking a look at this, How could we reframe ourselves to capture some of this market?

Archie is already a beast of a project, I think that it would be an excellent idea to think about cleaning up, simplifying, and unifying many of it's layers down to cleaner interfaces and even if it's starting at the most basic layers such as splitting the UI from the backend, turning the agent into a core runner, or even having 2 separate agent definitions, an AI agent and an agent runner. or. even better, an agent runner which utilises an open source project like Pi as the AI harness instead of having to code our own one.

I have Pi running now, with an immensely fast and capable model (deepseek-v4-flash-vision-exp) which is absolutely able to perform many actions at great speed, and that could assist us to getting this chunk of work done.

the idea of everything being pluggable services - making the entire solution modular, is exactly the goal I've had from the start. and it's already heading that way, but it's missing some guidance, assistance and care. I believe that a multi-agent system could help me get there, where I talk to an agent like claude, which spawns subagents to start implementation in isolation, and another agent monitors the work, assists, and carefully merges as the migration is in progress.

[01:23:10] sam@helix /work/apps/archie-core
> tree
(directory tree of archie-core omitted here -- see repository working tree at
the time of writing for the full listing; top-level shape was: AGENTS.md,
ARCHITECTURE.md, cmd/{archie-agent,archied,archie-playbooks}, deployments/,
docs/{architecture,archive,data,guides,prds,public}, examples/, extras/,
internal/{agentexec,app,channels,config,container,daemon,domain,eventbus,
events,forge,forgerpc,gate,gateway,indexing,infrastructure,installtype,
logging,memory,natsrpc,pairing,plugin,ratelimit,releaseannounce,
releaseupdate,secret,skill,skillscript,storage,store,storerpc,taskrun,
taskstate,tools,webhookguard,webui,worktree,worktreerpc,yaegiutil}, scripts/,
tools/, ui/{dist,src,test})
[01:23:12] sam@helix /work/apps/archie-core

---

One other thing I've grabbed inspiration from the Ansible project and I'm hoping to splice in - is the task.py (and likely more items) definitions; I think that being able to use existing ansible playbooks (and eventually roles/etc) within archie, would be astoundingly benefitial for engagement.

(ansible/playbook/task.py, GPLv3, Michael DeHaan et al. -- full file pasted in
chat; omitted here as reference-only source code, not reproduced verbatim in
this repository. It defines Ansible's `Task` class: FieldAttribute-based
declarative fields such as `args`, `action`, `loop`, `register`,
`changed_when`/`failed_when`/`until`, `delay`/`poll`/`retries`,
`loop_control`; `preprocess_data` which runs `ModuleArgsParser` to resolve the
legacy free-form task shape into `(action, args, delegate_to)`; `post_validate`
which templates fields through Jinja2 (`TemplateEngine`); parent-chain
attribute inheritance (`_get_parent_attribute` walking up through Block /
TaskInclude / Play); and `_resolve_conditional` for `when:` evaluation.)

---

Attached image: "AAP Pluggable Architecture" diagram -- a decomposition tree
with "AWX Decomposition" as root, branching to: Credentials, Subscription/
Billing, Auth (children: RBAC, SSO, Policy/ABAC), Communication (child:
Inter Service, and Execution (mesh) linked back into Runtime), Telemetry,
Inventory, Runtime (child: Scheduler), Content (child: Projects).
